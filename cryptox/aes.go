// Package cryptox 前端通讯加密与数据库字段加密的公司统一封装（AES-256-GCM）。
//
// # 密文格式契约
//
// Encrypt 的输出格式：
//
//	"v1:" + base64( nonce[12] + tag[16] + ciphertext )
//
// 各段说明：
//   - nonce：12 字节随机数（每次加密新生成）
//   - tag：16 字节 GCM 认证标签
//   - ciphertext：密文（长度与明文字节相同）
//
// 版本前缀：自 v1 起密文带 "v1:" 前缀（base64 标准字符集不含冒号，前缀无歧义）。
// Decrypt 同时接受带前缀与无前缀（v0.0.3 裸格式，字节排列与 v1 相同）的输入；
// Encrypt 只输出带前缀的格式。
//
// 密钥派生规则（两端必须一致）：
//   - 密钥 ≥32 字节（主路径）：直接取前 32 字节作为 AES-256 密钥，不涉及派生，
//     无需 Salt、无需任何初始化；
//   - 密钥 <32 字节：以 PBKDF2-SHA256(key, salt, iterations) 派生 32 字节密钥，
//     salt 必须已通过 SetConfig 显式配置，否则 Encrypt/Decrypt 返回错误
//     （iterations 默认 600000，SetConfig 可覆盖）。该路径已废弃，见下。
//
// 密钥与盐的职责边界：密钥是秘密，由调用方从环境等渠道注入（进入代码库即视为
// 泄露）；Salt 是公开的 KDF 参数，职责是防预计算表，不承担保密。「公开常量当
// 密钥、把 Salt 当秘密注入」是曾出现的误用形态（详见 docs/cryptox.md 维护者注记）。
//
// 短密钥（<32 字节）PBKDF2 派生路径已废弃：上游 Laravel Encrypter 是严格密钥
// 长度纪律、无 KDF 的形态，PBKDF2+盐来自历史复写偏差。自 2026-09-01 起文档级
// 废弃（运行时行为不变），计划 v1.0 移除——新接入一律使用 ≥32 字节随机密钥。
//
// 前端 JS 对接要点（解密视角）：
//  1. 剥离 "v1:" 前缀；
//  2. base64 标准解码剩余部分；
//  3. 按 [0:12] 取 nonce、[12:28] 取 tag、[28:] 取 ciphertext；
//  4. AES-256-GCM 解密（密钥按上述派生规则得到）。
//
// 跨语言实现以 docs/cryptox.md 第 4 节的黄金测试向量对账（固定 key/明文/密文
// 三元组，解密方向可复验）。
package cryptox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/pbkdf2"
)

// versionPrefix 当前密文版本前缀（v1 起；base64 标准字符集不含冒号，无歧义）
const versionPrefix = "v1:"

// errSaltNotConfigured 短密钥路径未配置 Salt 时的错误
var errSaltNotConfigured = errors.New("使用 <32 字节密钥时必须配置 Salt：请调用 SetConfig 设置 Salt，或改用 ≥32 字节密钥")

// Config AES 加密配置（调用方初始化时通过 SetConfig 设置）。
// 两个字段均只作用于已废弃的短密钥（<32 字节）派生路径；≥32 字节密钥的
// 主路径不读取任何配置，见各字段 Deprecated 说明。
type Config struct {
	// Salt PBKDF2 盐（<32 字节密钥路径必填；无默认值，不配置则报错）。
	//
	// Deprecated: 短密钥派生路径已废弃（计划 v1.0 移除）。Salt 是公开的 KDF
	// 参数、不承担保密（防预计算表用）；≥32 字节密钥路径不经过 Salt。
	Salt string

	// Iterations PBKDF2 迭代次数（默认 600000，SetConfig 可覆盖）。
	//
	// Deprecated: 仅作用于已废弃的短密钥派生路径，随该路径一并计划 v1.0 移除。
	Iterations int
}

// config 包级全局配置。Salt 零值为 ""，表示未配置（短密钥路径将拒绝工作）
var config = Config{
	Iterations: 600000,
}

// SetConfig 设置全局 AES 配置（项目 boot 层初始化时调用）。
// 零值字段跳过更新（Salt==""、Iterations<=0 不生效），语义与历版一致。
// 仅服务于已废弃的短密钥（<32 字节）派生路径——≥32 字节密钥的主路径
// 不依赖本调用，可直接使用 Encrypt/Decrypt。
func SetConfig(cfg Config) {
	if cfg.Salt != "" {
		config.Salt = cfg.Salt
	}
	if cfg.Iterations > 0 {
		config.Iterations = cfg.Iterations
	}
}

// Encrypt AES-256-GCM 加密，返回 "v1:" + base64(nonce + tag + ciphertext)
func Encrypt(plaintext string, key string) (string, error) {
	keyBytes, err := deriveKey(key)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(keyBytes)
	if err != nil {
		return "", err
	}
	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonceSize := aesgcm.NonceSize() // 12 bytes for GCM
	nonce := make([]byte, nonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	// Go GCM Seal 返回 nonce + ciphertext + tag（tag 追加在末尾）
	// 重排列为契约格式：nonce + tag + ciphertext。
	// 必须向独立缓冲区追加：sealed 的子切片 cap 共享底层数组，直接 append 会把 tag
	// 就地写进 ciphertext 区域，破坏密文（曾导致 round-trip 解密必然失败）
	sealed := aesgcm.Seal(nonce, nonce, []byte(plaintext), nil)
	result := make([]byte, 0, len(sealed))
	result = append(result, sealed[:nonceSize]...)
	result = append(result, sealed[len(sealed)-16:]...)
	result = append(result, sealed[nonceSize:len(sealed)-16]...)
	return versionPrefix + base64.StdEncoding.EncodeToString(result), nil
}

// Decrypt AES-256-GCM 解密，接受 "v1:" 前缀与无前缀（v0.0.3 裸格式）两种输入
func Decrypt(encrypted string, key string) (string, error) {
	keyBytes, err := deriveKey(key)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(keyBytes)
	if err != nil {
		return "", err
	}
	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	// 剥离版本前缀；无前缀输入（v0.0.3 裸格式）视同 v1 排列
	payload := strings.TrimPrefix(encrypted, versionPrefix)
	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return "", err
	}
	nonceSize := aesgcm.NonceSize() // 12
	if len(data) < nonceSize+16 {
		return "", fmt.Errorf("密文太短")
	}
	// 契约格式：nonce[12] + tag[16] + ciphertext
	nonce := data[:12]
	tag := data[12:28]
	ciphertext := data[28:]
	// Go GCM Open 需要 ciphertext + tag 拼接（同样拷贝到独立缓冲区，避免 append 别名）
	gcmCiphertext := make([]byte, 0, len(ciphertext)+len(tag))
	gcmCiphertext = append(gcmCiphertext, ciphertext...)
	gcmCiphertext = append(gcmCiphertext, tag...)
	plaintext, err := aesgcm.Open(nil, nonce, gcmCiphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

// IsEncrypted 判断字符串是否为本包加密格式（v1: 前缀与 v0.0.3 裸格式均识别）。
// base64 解码后至少 29 字节：nonce[12] + tag[16] + 至少 1 字节 ciphertext
func IsEncrypted(s string) bool {
	if strings.HasPrefix(s, versionPrefix) {
		s = s[len(versionPrefix):]
	}
	data, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return false
	}
	return len(data) >= 29
}

// deriveKey 密钥派生：
// ≥32 字节直接取前 32 字节（主路径）；<32 字节走 PBKDF2-SHA256(salt, iterations)
// 派生 32 字节，此时 Salt 必须已配置，否则返回 errSaltNotConfigured。
// 短密钥分支已废弃（2026-09-01 起，计划 v1.0 移除），仅维持存量行为不变。
func deriveKey(key string) ([]byte, error) {
	if len(key) >= 32 {
		return []byte(key[:32]), nil
	}
	if config.Salt == "" {
		return nil, errSaltNotConfigured
	}
	return pbkdf2.Key([]byte(key), []byte(config.Salt), config.Iterations, 32, sha256.New), nil
}
