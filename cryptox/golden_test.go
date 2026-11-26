package cryptox

import (
	"os"
	"strings"
	"testing"
)

// goldenVectors 黄金测试向量：跨语言契约锚（docs/cryptox.md 第 4 节同表）。
// 密文由本包 Encrypt 生成后固化（内嵌随机 nonce——向量锁的是前缀、字节排列、
// 截断语义与解密方向，不要求密文逐字节可复现）；任何排列、派生或前缀的改动
// 都会使 TestGoldenVectors 变红。全部采用 ≥32 字节密钥（主路径），不依赖 Salt。
var goldenVectors = []struct {
	name      string
	key       string
	plaintext string
	cipher    string
	bare      bool // true = v0.0.3 裸格式（无 v1: 前缀），验证 Decrypt 兼容路径
}{
	{
		name:      "32字节ASCII密钥",
		key:       "0123456789abcdef0123456789abcdef",
		plaintext: "shw-cryptox golden vector #1",
		cipher:    "v1:0/zbcGlse7IH3QXdVK54aigtvtXrlHhhWTEvC0eCQXNUYP72DLsVZq4MgofVFSBN6//N0FFdblU=",
	},
	{
		name:      "47字符密钥取前32",
		key:       "golden-vector-key-2-base64-shape-aaaaaaaaaaaa==",
		plaintext: "黄金向量二：中文明文与多字节字符",
		cipher:    "v1:5QXx8+t07wh/5FO4kIYrAXEj1T5EL+shHak6z4Of1MWRSQlKZk12cWREzAyoc09V8T+mnEWgdhVxrGHeHePTwP9HA+LoR4mGSH4hog==",
	},
	{
		name:      "v0.0.3裸格式无前缀",
		key:       "golden-vector-key-3-bare-format-40-bytes-xxxx",
		plaintext: "bare format vector",
		cipher:    "otIHpeQaiKtqEvOLfHRcV3aP7xPwFF8nS3J25JdOeHiF15pxHc3nNTj+2UtBdQ==",
		bare:      true,
	},
}

// TestGoldenVectors 黄金向量回归锁：向量必须可被 IsEncrypted 识别且解密还原明文。
func TestGoldenVectors(t *testing.T) {
	for _, v := range goldenVectors {
		t.Run(v.name, func(t *testing.T) {
			if len(v.key) < 32 {
				t.Fatalf("向量密钥长度不足 32 字节（当前 %d），主路径向量不允许", len(v.key))
			}
			if v.cipher == "PENDING" || v.cipher == "" {
				t.Fatalf("向量密文未固化（PENDING）——先以 GEN_GOLDEN=1 运行生成器回填")
			}
			if v.bare && strings.HasPrefix(v.cipher, versionPrefix) {
				t.Fatalf("裸格式向量不应带 %q 前缀", versionPrefix)
			}
			if !v.bare && !strings.HasPrefix(v.cipher, versionPrefix) {
				t.Fatalf("带前缀向量缺少 %q 前缀", versionPrefix)
			}
			if !IsEncrypted(v.cipher) {
				t.Fatalf("密文未被 IsEncrypted 识别: %s", v.cipher)
			}
			dec, err := Decrypt(v.cipher, v.key)
			if err != nil {
				t.Fatalf("黄金向量解密失败: %v", err)
			}
			if dec != v.plaintext {
				t.Fatalf("黄金向量明文不一致:\n期望 %q\n实际 %q", v.plaintext, dec)
			}
		})
	}
}

// TestGenerateGoldenVectors 一次性向量生成器：日常 Skip，仅 GEN_GOLDEN=1 时启用。
// 契约升版（v2: 等）重铸向量时远程执行：
//
//	GEN_GOLDEN=1 go test ./cryptox/ -run TestGenerateGoldenVectors -v -count=1
//
// 将输出的 cipher 回填 goldenVectors（bare 向量回填剥掉前缀的形态）。
func TestGenerateGoldenVectors(t *testing.T) {
	if os.Getenv("GEN_GOLDEN") == "" {
		t.Skip("生成器默认跳过：设置 GEN_GOLDEN=1 显式启用")
	}
	for _, v := range goldenVectors {
		enc, err := Encrypt(v.plaintext, v.key)
		if err != nil {
			t.Fatalf("向量 %s 生成失败: %v", v.name, err)
		}
		if v.bare {
			enc = strings.TrimPrefix(enc, versionPrefix)
		}
		t.Logf("VECTOR[%s]\n  key:       %q\n  plaintext: %q\n  cipher:    %q", v.name, v.key, v.plaintext, enc)
	}
}
