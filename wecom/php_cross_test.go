package wecom

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

// phpFixture hyperf-lib PHP 侧 WXBizMsgCrypt 生成的固定密文夹具
// （生成方式：docker php:8 加载 hyperf-lib/src/Provider/Wecom/Crypt 六类，
// 以本文件内固定 token/AESKey/corpId/明文生成；同一次运行亦用 PHP Prpcrypt
// 解密了 Go 侧密文并校验一致——见 phpVerified 字段）
type phpFixture struct {
	Token        string `json:"token"`
	AesKey       string `json:"aesKey"`
	CorpID       string `json:"corpId"`
	Plaintext    string `json:"plaintext"`
	Encrypted    string `json:"encrypted"`
	Timestamp    string `json:"timestamp"`
	Nonce        string `json:"nonce"`
	MsgSignature string `json:"msgSignature"`

	// 以下字段由生成脚本在同一次运行中回写：Go 生成的密文经 PHP 解密的结果
	GoEncrypted  string `json:"goEncrypted"`
	GoPlaintext  string `json:"goPlaintext"`
	PhpDecrypted string `json:"phpDecrypted"`
	PhpVerified  bool   `json:"phpVerified"`
}

func loadPHPFixture(t *testing.T) *phpFixture {
	t.Helper()
	raw, err := os.ReadFile("testdata/wecom_php_fixture.json")
	if err != nil {
		t.Fatalf("读取 PHP 夹具失败: %v", err)
	}
	var f phpFixture
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("解析夹具失败: %v", err)
	}
	return &f
}

// TestCrossPHP_DecryptPHPCiphertext Go 解密 PHP 生成的固定密文：明文一致
func TestCrossPHP_DecryptPHPCiphertext(t *testing.T) {
	f := loadPHPFixture(t)
	c, err := NewWXBizMsgCrypt(f.Token, f.AesKey, f.CorpID)
	if err != nil {
		t.Fatal(err)
	}

	got, err := c.Decrypt(f.Encrypted)
	if err != nil {
		t.Fatalf("Go 解密 PHP 密文失败: %v", err)
	}
	if got != f.Plaintext {
		t.Fatalf("解密明文与 PHP 侧明文不一致:\n got=%q\nwant=%q", got, f.Plaintext)
	}

	// decryptEvent 全链（验签 + XML 提取 + 解密）：签名由 PHP SHA1 独立生成
	body := fmt.Sprintf(`<xml><Encrypt><![CDATA[%s]]></Encrypt></xml>`, f.Encrypted)
	plain, err := c.DecryptEvent(f.MsgSignature, f.Timestamp, f.Nonce, body)
	if err != nil {
		t.Fatalf("DecryptEvent 应通过 PHP 生成的签名: %v", err)
	}
	if plain != f.Plaintext {
		t.Fatalf("DecryptEvent 明文不符: %q", plain)
	}
}

// TestCrossPHP_GoEncryptPHPDecrypt Go 加密输出可被 PHP 实现解密回原文
// （phpVerified 由生成脚本在同一次 docker 运行中以 PHP Prpcrypt 解密
// GoEncrypted 并与 GoPlaintext 比对后写入；此处断言该记录完整且为真，
// 并在 Go 侧复核：Go 能解出自己的密文、且与送检明文一致）
func TestCrossPHP_GoEncryptPHPDecrypt(t *testing.T) {
	f := loadPHPFixture(t)
	if !f.PhpVerified {
		t.Fatal("夹具缺少 PHP 解密 Go 密文的成功记录（需重新生成 testdata）")
	}
	if f.GoPlaintext != f.Plaintext {
		t.Fatal("送检明文与夹具明文不一致")
	}
	if f.PhpDecrypted != f.Plaintext {
		t.Fatalf("PHP 解密结果与明文不符: %q", f.PhpDecrypted)
	}

	// Go 侧复核：同一密文 Go 也能解（PHP 接受 ⇒ 结构合法；Go 解回 ⇒ 双实现等价）
	c, err := NewWXBizMsgCrypt(f.Token, f.AesKey, f.CorpID)
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.Decrypt(f.GoEncrypted)
	if err != nil {
		t.Fatalf("Go 解密送检密文失败: %v", err)
	}
	if got != f.Plaintext {
		t.Fatalf("Go 解密送检密文明文不符: %q", got)
	}
}

// TestCrossPHP_VerifyUrlSignatureWithPHPSignature verifyUrl 用 PHP 生成的签名四元组通过
func TestCrossPHP_VerifyUrlSignatureWithPHPSignature(t *testing.T) {
	f := loadPHPFixture(t)
	c, _ := NewWXBizMsgCrypt(f.Token, f.AesKey, f.CorpID)
	plain, err := c.VerifyUrl(f.MsgSignature, f.Timestamp, f.Nonce, f.Encrypted)
	if err != nil || plain != f.Plaintext {
		t.Fatalf("verifyUrl 应以 PHP 签名通过: %v %q", err, plain)
	}
	// 篡改签名拒绝
	if _, err := c.VerifyUrl("bad"+f.MsgSignature[:8], f.Timestamp, f.Nonce, f.Encrypted); err == nil {
		t.Fatal("篡改签名应拒绝")
	}
}
