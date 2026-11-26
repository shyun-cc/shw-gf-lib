package wxpay

import (
	"crypto"
	"crypto/rsa"
	"fmt"
)

// BizError V3 业务异常：HTTP 状态码 ≥400 或应答体含 code。与微信系 JSON errcode 形态不同，
// V3 错误是 HTTP 状态码 + body {code,message}——Code/Message 原值携带，errors.As 可捕获。
type BizError struct {
	Code       string
	Message    string
	HTTPStatus int
}

func (e *BizError) Error() string {
	return fmt.Sprintf("wxpay: 业务错误 http=%d code=%s message=%s", e.HTTPStatus, e.Code, e.Message)
}

// InfraError 基础设施异常：网络/解码/签名/验签/解密链路失败。
type InfraError struct {
	Op  string // 出错环节：http / decode / encode / sign / verify / decrypt
	Err error
}

func (e *InfraError) Error() string {
	return fmt.Sprintf("wxpay: 基础设施错误 op=%s: %v", e.Op, e.Err)
}

func (e *InfraError) Unwrap() error { return e.Err }

// rsaVerifyPKCS1v15 RSA-SHA256 验签（SHA256withRSA / PKCS1v15）
func rsaVerifyPKCS1v15(pub *rsa.PublicKey, digest, sig []byte) error {
	return rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest, sig)
}

// nowUnix 时间戳注入位（测试固定化用）
var nowUnix = func() int64 { return timeNow().Unix() }
