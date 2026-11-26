// Package wxpay 微信支付商户侧服务端 API 自主复刻（零厂商 SDK），V2+V3 双轨。
// 本批为 V3 轨（2026-08-22，change wxpay-v3-basic）：WECHATPAY2-SHA256-RSA2048 请求签名、
// 平台证书/微信支付公钥验签、回调 AES-256-GCM 解密、基础支付族 7 端点。
// V2 轨（XML + API 密钥 MD5/HMAC-SHA256 + p12 双向证书）与 V3 余域（退款/账单/分账/转账/
// 支付分/营销/投诉/合单余变体）在挂起清单，由后续 change 逐页取证铺设。
// 一切证书/密钥经构造参数注入，包内零硬编码。
package wxpay

import (
	"crypto/rsa"
	"net/http"
)

// Credentials 商户凭证——全部经构造注入（库不做项目决策）
type Credentials struct {
	MchID      string                    // 商户号
	AppID      string                    // 应用 ID
	SerialNo   string                    // 商户 API 证书序列号
	PrivateKey *rsa.PrivateKey           // 商户 API 证书私钥（apiclient_key）
	APIv3Key   []byte                    // APIv3 密钥（32 字节，回调解密用）
	VerifyKeys map[string]*rsa.PublicKey // 验签公钥池：平台证书公钥或微信支付公钥，键为序列号
}

// Client 微信支付 V3 客户端
type Client struct {
	Creds      Credentials
	HTTPClient *http.Client
	BaseURL    string // 默认 https://api.mch.weixin.qq.com
}

const defaultMchAPI = "https://api.mch.weixin.qq.com"

// NewClient 构造 V3 客户端
func NewClient(creds Credentials) *Client {
	return &Client{Creds: creds}
}

// NotifyResource 回调/证书下载接口的加密资源结构（官方 AEAD_AES_256_GCM 形态）
type NotifyResource struct {
	OriginalType   string `json:"original_type"`
	Algorithm      string `json:"algorithm"`
	Ciphertext     string `json:"ciphertext"`
	Nonce          string `json:"nonce"`
	AssociatedData string `json:"associated_data"`
}
