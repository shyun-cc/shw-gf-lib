package wxpay

import (
	"crypto"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"strings"
)

// buildMessageString 请求签名串（官方 5 行，每行尾含 \n）：
// HTTP方法\n URL\n 时间戳\n 随机串\n 报文主体\n（GET 主体为空）
func buildMessageString(method, pathWithQuery, timestamp, nonce, body string) string {
	return method + "\n" + pathWithQuery + "\n" + timestamp + "\n" + nonce + "\n" + body + "\n"
}

// buildVerifyString 应答/回调验签串（官方 3 行，每行尾含 \n）：时间戳\n 随机串\n 主体\n
func buildVerifyString(timestamp, nonce, body string) string {
	return timestamp + "\n" + nonce + "\n" + body + "\n"
}

func randomNonce() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "nonce-error"
	}
	return fmt.Sprintf("%x", b)
}

// AuthorizationHeader 构造请求 Authorization 头：
// WECHATPAY2-SHA256-RSA2048 mchid="…",nonce_str="…",timestamp="…",serial_no="…",signature="…"
func (c *Client) AuthorizationHeader(method, pathWithQuery, body string) (string, error) {
	ts := fmt.Sprintf("%d", nowUnix())
	nonce := randomNonce()
	msg := buildMessageString(strings.ToUpper(method), pathWithQuery, ts, nonce, body)
	digest := sha256.Sum256([]byte(msg))
	sig, err := c.Creds.PrivateKey.Sign(nil, digest[:], crypto.SHA256)
	if err != nil {
		return "", &InfraError{Op: "encode", Err: fmt.Errorf("请求签名失败: %w", err)}
	}
	return fmt.Sprintf(`WECHATPAY2-SHA256-RSA2048 mchid="%s",nonce_str="%s",timestamp="%s",serial_no="%s",signature="%s"`,
		c.Creds.MchID, nonce, ts, c.Creds.SerialNo, base64.StdEncoding.EncodeToString(sig)), nil
}

// VerifyResponse 应答/回调验签：3 行验签串以 Wechatpay-Serial 对应公钥验证 Wechatpay-Signature。
// 与 DecryptResource 为两个独立入口（回调验签与业务解密分离）。
func (c *Client) VerifyResponse(timestamp, nonce, body, signature, serial string) error {
	pub, ok := c.Creds.VerifyKeys[serial]
	if !ok {
		return &InfraError{Op: "verify", Err: fmt.Errorf("无序列号 %s 的验签公钥（补 VerifyKeys）", serial)}
	}
	sig, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return &InfraError{Op: "verify", Err: fmt.Errorf("签名 base64 解码失败: %w", err)}
	}
	digest := sha256.Sum256([]byte(buildVerifyString(timestamp, nonce, body)))
	if err := rsaVerifyPKCS1v15(pub, digest[:], sig); err != nil {
		return &InfraError{Op: "verify", Err: fmt.Errorf("验签失败: %w", err)}
	}
	return nil
}

// NotifyRequest 微信支付回调通知的顶层结构（验签用原始字段 + 加密资源）
type NotifyRequest struct {
	ID           string         `json:"id"`
	CreateTime   string         `json:"create_time"`
	ResourceType string         `json:"resource_type"`
	EventType    string         `json:"event_type"`
	Summary      string         `json:"summary"`
	Resource     NotifyResource `json:"resource"`
}

// VerifyNotify 回调完整入口：按官方头验签（Wechatpay-Timestamp/Nonce/Signature/Serial）。
// 业务解密另走 DecryptResource——两入口独立可调。
func (c *Client) VerifyNotify(timestamp, nonce, body, signature, serial string) error {
	return c.VerifyResponse(timestamp, nonce, body, signature, serial)
}

var _ = x509.MarshalPKIXPublicKey // 保留引用位：公钥解析由调用方注入 *rsa.PublicKey
