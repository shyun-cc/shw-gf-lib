package wecom

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"testing"
)

// 固定测试参数（43 位 EncodingAESKey）
const (
	testToken  = "testToken123"
	testCorpID = "wx_corp_id_1"
	testAESKey = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFG" // 43 位
)

// TestCrypt_RoundTrip 加解密 round-trip（中文 + XML 明文）
func TestCrypt_RoundTrip(t *testing.T) {
	c, err := NewWXBizMsgCrypt(testToken, testAESKey, testCorpID)
	if err != nil {
		t.Fatal(err)
	}
	for _, plain := range []string{
		"hello",
		"中文明文测试",
		`<xml><ToUserName><![CDATA[corp]]></ToUserName><Event>change_contact</Event></xml>`,
		"", // 空明文
	} {
		enc, err := c.Encrypt(plain)
		if err != nil {
			t.Fatalf("加密 %q 失败: %v", plain, err)
		}
		got, err := c.Decrypt(enc)
		if err != nil {
			t.Fatalf("解密 %q 失败: %v", plain, err)
		}
		if got != plain {
			t.Fatalf("round-trip 不符: got=%q want=%q", got, plain)
		}
	}
}

// TestCrypt_SignatureIndependentRecompute SHA1 签名独立重算（四元素字典序）
func TestCrypt_SignatureIndependentRecompute(t *testing.T) {
	token, ts, nonce, enc := "bToken", "1723963200", "n0nce", "ENCRYPTED=="
	// 独立实现：sort(SORT_STRING) + sha1
	arr := []string{enc, token, ts, nonce}
	sort.Strings(arr)
	h := sha1.Sum([]byte(strings.Join(arr, "")))
	want := hex.EncodeToString(h[:])
	if got := signatureSHA1(token, ts, nonce, enc); got != want {
		t.Fatalf("签名不符:\n got=%s\nwant=%s", got, want)
	}
}

// TestCrypt_VerifyUrl verifyUrl：合法四元组通过、篡改任一失败
func TestCrypt_VerifyUrl(t *testing.T) {
	c, err := NewWXBizMsgCrypt(testToken, testAESKey, testCorpID)
	if err != nil {
		t.Fatal(err)
	}
	echoPlain := "echo-plain-text"
	echostr, err := c.Encrypt(echoPlain)
	if err != nil {
		t.Fatal(err)
	}
	ts, nonce := "1723963200", "n1"
	sig := signatureSHA1(testToken, ts, nonce, echostr)

	got, err := c.VerifyUrl(sig, ts, nonce, echostr)
	if err != nil || got != echoPlain {
		t.Fatalf("合法四元组应通过: %v %q", err, got)
	}

	badCases := []struct {
		name                            string
		msgSig, timestamp, nonce2, echo string
	}{
		{"签名篡改", "bad" + sig[3:], ts, nonce, echostr},
		{"时间戳篡改", sig, "9999999999", nonce, echostr},
		{"nonce篡改", sig, ts, "tampered", echostr},
		{"echostr篡改", sig, ts, nonce, echostr[:len(echostr)-2] + "xy"},
	}
	for _, bc := range badCases {
		if _, err := c.VerifyUrl(bc.msgSig, bc.timestamp, bc.nonce2, bc.echo); err == nil {
			t.Fatalf("篡改%s应失败", bc.name)
		}
	}
}

// TestCrypt_CorpIDTailCheck 尾校验：CorpID 不符解密失败
func TestCrypt_CorpIDTailCheck(t *testing.T) {
	// 用 corpID A 加密、以 corpID B 解密
	ca, _ := NewWXBizMsgCrypt(testToken, testAESKey, "corpA")
	cb, _ := NewWXBizMsgCrypt(testToken, testAESKey, "corpB")
	enc, err := ca.Encrypt("payload")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cb.Decrypt(enc); err != ErrValidateCorpID {
		t.Fatalf("应报尾校验失败，实际 %v", err)
	}
}

// TestDecryptEvent_XMLExtract DecryptEvent 从 XML 提取 + 验签 + 解密
func TestDecryptEvent_XMLExtract(t *testing.T) {
	c, _ := NewWXBizMsgCrypt(testToken, testAESKey, testCorpID)
	plain := `<xml><Event>change_contact</Event></xml>`
	enc, _ := c.Encrypt(plain)
	ts, nonce := "1723963200", "n2"
	sig := signatureSHA1(testToken, ts, nonce, enc)
	body := fmt.Sprintf(`<xml><Encrypt><![CDATA[%s]]></Encrypt></xml>`, enc)

	got, err := c.DecryptEvent(sig, ts, nonce, body)
	if err != nil || got != plain {
		t.Fatalf("DecryptEvent 应成功: %v %q", err, got)
	}

	// 签名错误拒绝
	if _, err := c.DecryptEvent("bad"+sig[3:], ts, nonce, body); err == nil {
		t.Fatal("签名错误应拒绝")
	}
	// 无 Encrypt 节点拒绝
	if _, err := c.DecryptEvent(sig, ts, nonce, "<xml></xml>"); err != ErrNoEncryptNode {
		t.Fatalf("缺 Encrypt 应报错，实际 %v", err)
	}
}

// TestEncryptMsg_Envelope EncryptMsg 输出含 Encrypt/MsgSignature/TimeStamp/Nonce
func TestEncryptMsg_Envelope(t *testing.T) {
	c, _ := NewWXBizMsgCrypt(testToken, testAESKey, testCorpID)
	ts, nonce := "1723963200", "n3"
	envelope, err := c.EncryptMsg("reply", ts, nonce)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<Encrypt>", "<MsgSignature>", "<TimeStamp>1723963200</TimeStamp>", "<Nonce>"} {
		if !strings.Contains(envelope, want) {
			t.Fatalf("信封缺 %s: %s", want, envelope)
		}
	}
}

// TestNewCrypt_IllegalKey 43 位以外的 AESKey 拒绝
func TestNewCrypt_IllegalKey(t *testing.T) {
	if _, err := NewWXBizMsgCrypt("t", "shortkey", "corp"); err != ErrIllegalAESKey {
		t.Fatalf("短 key 应拒绝，实际 %v", err)
	}
}
