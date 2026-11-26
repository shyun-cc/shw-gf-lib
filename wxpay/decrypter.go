package wxpay

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"fmt"
	"time"
)

var timeNow = time.Now

// DecryptResource 回调/证书下载资源的 AES-256-GCM 解密（独立入口，与 VerifyResponse 分离）：
// APIv3 密钥（32 字节）、ciphertext Base64 解码后为 C‖T（T=末 16 字节 Tag）、nonce 为 IV、
// associated_data 为 AAD；明文为 UTF-8 JSON 字符串。
func (c *Client) DecryptResource(res NotifyResource) ([]byte, error) {
	if len(c.Creds.APIv3Key) != 32 {
		return nil, &InfraError{Op: "decrypt", Err: fmt.Errorf("APIv3 密钥须 32 字节，实际 %d", len(c.Creds.APIv3Key))}
	}
	raw, err := base64.StdEncoding.DecodeString(res.Ciphertext)
	if err != nil {
		return nil, &InfraError{Op: "decrypt", Err: fmt.Errorf("ciphertext base64 解码失败: %w", err)}
	}
	if len(raw) < 16 {
		return nil, &InfraError{Op: "decrypt", Err: fmt.Errorf("密文长度 %d 不足（C‖T 至少 16 字节 Tag）", len(raw))}
	}
	block, err := aes.NewCipher(c.Creds.APIv3Key)
	if err != nil {
		return nil, &InfraError{Op: "decrypt", Err: err}
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, &InfraError{Op: "decrypt", Err: err}
	}
	// Go GCM Open 期望 Ciphertext‖Tag 拼接——与官方 C‖T 序一致
	plain, err := gcm.Open(nil, []byte(res.Nonce), raw, []byte(res.AssociatedData))
	if err != nil {
		return nil, &InfraError{Op: "decrypt", Err: fmt.Errorf("GCM 解密失败（密钥/AAD/密文不匹配）: %w", err)}
	}
	return plain, nil
}
