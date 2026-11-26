package wxoa

import (
	"github.com/shyun-cc/shw-gf-lib/wecom"
)

// CallbackCrypt 公众号回调验签解密：复用 wecom 包 WXBizMsgCrypt（同一 AES-CBC/SHA1/PKCS7
// 算法栈），receiveid 语义第三参传公众号 AppId（公众号侧尾校验对象是 AppId 而非 CorpID）。
// wxoa→wecom 编译依赖由此建立（go list -deps 可取证）。
type CallbackCrypt struct {
	inner *wecom.WXBizMsgCrypt
}

// NewCallbackCrypt 构造（token/encodingAESKey 来自公众号后台「服务器配置」，appID 为尾校验对象）
func NewCallbackCrypt(appID, token, encodingAESKey string) (*CallbackCrypt, error) {
	inner, err := wecom.NewWXBizMsgCrypt(token, encodingAESKey, appID)
	if err != nil {
		return nil, err
	}
	return &CallbackCrypt{inner: inner}, nil
}

// VerifyURL 服务器配置验证：验签解密 echostr 返回明文回执
func (c *CallbackCrypt) VerifyURL(msgSignature, timestamp, nonce, echostr string) (string, error) {
	return c.inner.VerifyUrl(msgSignature, timestamp, nonce, echostr)
}

// DecryptEvent 验签解密回调密文（XML 明文）
func (c *CallbackCrypt) DecryptEvent(msgSignature, timestamp, nonce, encryptedXML string) (string, error) {
	return c.inner.DecryptEvent(msgSignature, timestamp, nonce, encryptedXML)
}

// EncryptMsg 被动回复消息加密
func (c *CallbackCrypt) EncryptMsg(replyMsg, timestamp, nonce string) (string, error) {
	return c.inner.EncryptMsg(replyMsg, timestamp, nonce)
}
