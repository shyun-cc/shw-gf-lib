package cryptox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"golang.org/x/crypto/pbkdf2"
)

// setSaltForTest 临时设置全局 Salt，测试结束后还原（全局 config 操纵纪律：保存/修改/还原）
func setSaltForTest(t *testing.T, salt string) {
	t.Helper()
	saved := config
	config = Config{Salt: salt, Iterations: saved.Iterations}
	t.Cleanup(func() { config = saved })
}

// clearSaltForTest 临时清空全局 Salt（模拟未配置），测试结束后还原
func clearSaltForTest(t *testing.T) {
	t.Helper()
	saved := config
	config = Config{Iterations: saved.Iterations}
	t.Cleanup(func() { config = saved })
}

// TestEncryptDecryptRoundTrip 加密后解密必须还原明文（回归：append 别名污染曾导致必然失败）
func TestEncryptDecryptRoundTrip(t *testing.T) {
	setSaltForTest(t, "test-salt")
	cases := []struct {
		name      string
		plaintext string
		key       string
	}{
		{"短明文-短密钥", "hi", "k"},
		{"中文明文", "你好，世界", "test-key-123"},
		{"长明文-触发多段 append", strings.Repeat("A-Z拼一段超过16字节的明文！", 10), "another-key"},
		{"32字节以上密钥直接截断", "payload", strings.Repeat("x", 40)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			enc, err := Encrypt(c.plaintext, c.key)
			if err != nil {
				t.Fatalf("Encrypt 失败: %v", err)
			}
			if !IsEncrypted(enc) {
				t.Fatalf("IsEncrypted 未识别加密结果: %s", enc)
			}
			dec, err := Decrypt(enc, c.key)
			if err != nil {
				t.Fatalf("Decrypt 失败: %v", err)
			}
			if dec != c.plaintext {
				t.Fatalf("round-trip 不一致:\n期望 %q\n实际 %q", c.plaintext, dec)
			}
		})
	}
}

// TestDecryptWrongKey 错误密钥必须认证失败而非返回错乱明文
func TestDecryptWrongKey(t *testing.T) {
	setSaltForTest(t, "test-salt")
	enc, err := Encrypt("secret", "right-key")
	if err != nil {
		t.Fatalf("Encrypt 失败: %v", err)
	}
	if _, err := Decrypt(enc, "wrong-key"); err == nil {
		t.Fatal("错误密钥解密竟然成功，GCM 认证失效")
	}
}

// TestIsEncryptedRejectsShortInput 过短的输入不应被当作合法密文
func TestIsEncryptedRejectsShortInput(t *testing.T) {
	if IsEncrypted("hello") {
		t.Fatal("普通字符串不应被识别为加密格式")
	}
}

// TestEncryptV1PrefixFormat Encrypt 输出必须带 "v1:" 前缀且 IsEncrypted 识别
func TestEncryptV1PrefixFormat(t *testing.T) {
	setSaltForTest(t, "test-salt")
	// 短密钥（PBKDF2 路径）与长密钥（截断路径）均须带前缀
	for _, key := range []string{"short-key", strings.Repeat("x", 40)} {
		enc, err := Encrypt("payload", key)
		if err != nil {
			t.Fatalf("Encrypt 失败: %v", err)
		}
		if !strings.HasPrefix(enc, "v1:") {
			t.Fatalf("密文缺少 v1: 前缀: %s", enc)
		}
		if !IsEncrypted(enc) {
			t.Fatalf("IsEncrypted 未识别带前缀密文: %s", enc)
		}
	}
}

// legacyEncrypt 按 v0.0.3 实现（无前缀）的加密逻辑独立构造裸格式密文：
// 固定 nonce + 相同的 nonce+tag+ciphertext 字节排列。独立复现而非剥新密文的前缀，
// 才能真正验证 Decrypt 的无前缀兼容路径。
func legacyEncrypt(t *testing.T, plaintext, key string) string {
	t.Helper()
	// 短密钥路径：与生产派生规则一致的 PBKDF2-SHA256(salt, 600000)
	keyBytes := pbkdf2.Key([]byte(key), []byte("test-salt"), 600000, 32, sha256.New)
	block, err := aes.NewCipher(keyBytes)
	if err != nil {
		t.Fatalf("legacyEncrypt 构造失败: %v", err)
	}
	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("legacyEncrypt 构造失败: %v", err)
	}
	nonce := []byte("0123456789ab") // 固定 12 字节 nonce，构造确定性密文
	sealed := aesgcm.Seal(nonce, nonce, []byte(plaintext), nil)
	nonceSize := aesgcm.NonceSize()
	// v0.0.3 排列（与 v1 相同）：nonce + tag + ciphertext
	result := make([]byte, 0, len(sealed))
	result = append(result, sealed[:nonceSize]...)
	result = append(result, sealed[len(sealed)-16:]...)
	result = append(result, sealed[nonceSize:len(sealed)-16]...)
	return base64.StdEncoding.EncodeToString(result)
}

// TestDecryptLegacyBareFormat 无前缀（v0.0.3 裸格式）密文必须可解
func TestDecryptLegacyBareFormat(t *testing.T) {
	setSaltForTest(t, "test-salt")
	legacy := legacyEncrypt(t, "legacy-payload", "legacy-key")
	if strings.HasPrefix(legacy, "v1:") {
		t.Fatal("构造的裸格式密文不应带前缀")
	}
	dec, err := Decrypt(legacy, "legacy-key")
	if err != nil {
		t.Fatalf("无前缀密文解密失败: %v", err)
	}
	if dec != "legacy-payload" {
		t.Fatalf("无前缀密文解密不一致:\n期望 %q\n实际 %q", "legacy-payload", dec)
	}
}

// TestShortKeyRequiresSalt 未配 Salt 时短密钥路径 Encrypt/Decrypt 均须报错并给出指引
func TestShortKeyRequiresSalt(t *testing.T) {
	clearSaltForTest(t)
	_, err := Encrypt("data", "short-key")
	if !errors.Is(err, errSaltNotConfigured) {
		t.Fatalf("未配 Salt 加密应返回 errSaltNotConfigured，实际: %v", err)
	}
	_, err = Decrypt("v1:whatever", "short-key")
	if !errors.Is(err, errSaltNotConfigured) {
		t.Fatalf("未配 Salt 解密应返回 errSaltNotConfigured，实际: %v", err)
	}
}

// TestLongKeyWithoutSalt 长密钥（≥32 字节）不依赖 Salt：未配置也应正常加解密
func TestLongKeyWithoutSalt(t *testing.T) {
	clearSaltForTest(t)
	key := strings.Repeat("k", 32)
	enc, err := Encrypt("no-salt-needed", key)
	if err != nil {
		t.Fatalf("长密钥不应依赖 Salt，Encrypt 失败: %v", err)
	}
	dec, err := Decrypt(enc, key)
	if err != nil {
		t.Fatalf("长密钥不应依赖 Salt，Decrypt 失败: %v", err)
	}
	if dec != "no-salt-needed" {
		t.Fatalf("round-trip 不一致:\n期望 %q\n实际 %q", "no-salt-needed", dec)
	}
}

// TestIsEncryptedDualFormat IsEncrypted 须同时识别 v1 前缀与裸格式
func TestIsEncryptedDualFormat(t *testing.T) {
	setSaltForTest(t, "test-salt")
	enc, err := Encrypt("data", "short-key")
	if err != nil {
		t.Fatalf("Encrypt 失败: %v", err)
	}
	if !IsEncrypted(enc) {
		t.Fatal("带 v1: 前缀的密文未被识别")
	}
	legacy := legacyEncrypt(t, "data", "short-key")
	if !IsEncrypted(legacy) {
		t.Fatal("v0.0.3 裸格式密文未被识别")
	}
	// 剥掉前缀后 base64 非法的输入不应误判
	if IsEncrypted("v1:!!!not-base64!!!") {
		t.Fatal("前缀后跟非法 base64 的字符串不应被识别为密文")
	}
}
