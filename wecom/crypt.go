// Package wecom 企业微信 API 自主复刻（对应 PHP Provider/Wecom），零厂商 SDK。
//
// crypt.go：WXBizMsgCrypt——回调消息加解密与验签，逐字节对齐微信官方协议：
//   - 密钥：EncodingAESKey（43 位）补 "=" 后 base64 解码得 32 字节 AES key
//   - 明文结构：16B 随机串 + 4B 网络序明文长度 + 明文 + CorpID（尾校验）
//   - 填充：PKCS7 32 字节块对齐
//   - 加密：AES-256-CBC，IV = key 前 16 字节
//   - 签名：SHA1(sort([token, timestamp, nonce, encryptMsg]) 字典序拼接)
package wecom

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// PKCS7 块大小（微信协议固定 32 字节，非标准 AES 的 16）
const pkcs7BlockSize = 32

// crypt 层错误
var (
	ErrIllegalAESKey  = errors.New("wecom: 非法 AES Key（须 32 字节）")
	ErrDecodeBase64   = errors.New("wecom: base64 解码失败")
	ErrDecryptAES     = errors.New("wecom: AES 解密失败")
	ErrEncryptAES     = errors.New("wecom: AES 加密失败")
	ErrIllegalBuffer  = errors.New("wecom: 解密后 buffer 不合法")
	ErrValidateCorpID = errors.New("wecom: CorpID 尾校验失败")
	ErrSignMismatch   = errors.New("wecom: 签名不匹配")
	ErrNoEncryptNode  = errors.New("wecom: XML 中缺少 Encrypt 节点")
)

// WXBizMsgCrypt 企微回调加解密器
type WXBizMsgCrypt struct {
	token  string
	corpID string
	key    []byte // 32 字节
}

// NewWXBizMsgCrypt 构造：encodingAESKey 为 43 位 EncodingAESKey
func NewWXBizMsgCrypt(token, encodingAESKey, corpID string) (*WXBizMsgCrypt, error) {
	key, err := base64.StdEncoding.DecodeString(encodingAESKey + "=")
	if err != nil || len(key) != 32 {
		return nil, ErrIllegalAESKey
	}
	return &WXBizMsgCrypt{token: token, corpID: corpID, key: key}, nil
}

// VerifyUrl URL 验证（回调配置时企微 GET 校验）：签名通过后解密 echostr 返回明文
func (c *WXBizMsgCrypt) VerifyUrl(msgSignature, timestamp, nonce, echostr string) (string, error) {
	if err := c.verifySignature(msgSignature, timestamp, nonce, echostr); err != nil {
		return "", err
	}
	return c.Decrypt(echostr)
}

// DecryptEvent 解密事件回调：从 XML body 提取 Encrypt、验签、解密返回明文 XML
func (c *WXBizMsgCrypt) DecryptEvent(msgSignature, timestamp, nonce, encryptedXML string) (string, error) {
	encrypt, err := extractEncrypt(encryptedXML)
	if err != nil {
		return "", err
	}
	if err := c.verifySignature(msgSignature, timestamp, nonce, encrypt); err != nil {
		return "", err
	}
	return c.Decrypt(encrypt)
}

// EncryptMsg 加密被动回复消息：加密 + 签名，返回完整 XML 信封
func (c *WXBizMsgCrypt) EncryptMsg(replyMsg, timestamp, nonce string) (string, error) {
	encrypt, err := c.Encrypt(replyMsg)
	if err != nil {
		return "", err
	}
	sig := c.signature(timestamp, nonce, encrypt)
	return fmt.Sprintf(`<xml><Encrypt><![CDATA[%s]]></Encrypt><MsgSignature><![CDATA[%s]]></MsgSignature><TimeStamp>%s</TimeStamp><Nonce><![CDATA[%s]]></Nonce></xml>`,
		encrypt, sig, timestamp, nonce), nil
}

// Decrypt 解密单段密文（base64）：去 PKCS7 → 跳 16B 随机 → 读 4B 长度取明文 → 尾部 CorpID 校验
func (c *WXBizMsgCrypt) Decrypt(encrypted string) (string, error) {
	ciphertext, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		return "", ErrDecodeBase64
	}
	if len(ciphertext)%aes.BlockSize != 0 || len(ciphertext) < aes.BlockSize {
		return "", ErrIllegalBuffer
	}

	block, err := aes.NewCipher(c.key)
	if err != nil {
		return "", ErrIllegalAESKey
	}
	iv := c.key[:16]
	plaintext := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plaintext, ciphertext)

	unpadded := pkcs7Unpad(plaintext, pkcs7BlockSize)
	if len(unpadded) < 20 {
		return "", ErrIllegalBuffer
	}
	content := unpadded[16:]
	xmlLen := int(binary.BigEndian.Uint32(content[:4]))
	if xmlLen < 0 || 4+xmlLen > len(content) {
		return "", ErrIllegalBuffer
	}
	xmlContent := string(content[4 : 4+xmlLen])
	fromCorpID := string(content[4+xmlLen:])
	if fromCorpID != c.corpID {
		return "", ErrValidateCorpID
	}
	return xmlContent, nil
}

// Encrypt 加密明文（拼接 16B 随机 + 4B 长度 + 明文 + CorpID → PKCS7 → AES-CBC → base64）
func (c *WXBizMsgCrypt) Encrypt(text string) (string, error) {
	var packed []byte
	packed = append(packed, make([]byte, 16)...) // 随机前缀
	if _, err := rand.Read(packed[:16]); err != nil {
		return "", ErrEncryptAES
	}
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(text)))
	packed = append(packed, lenBuf[:]...)
	packed = append(packed, text...)
	packed = append(packed, c.corpID...)

	padded := pkcs7Pad(packed, pkcs7BlockSize)
	block, err := aes.NewCipher(c.key)
	if err != nil {
		return "", ErrIllegalAESKey
	}
	ciphertext := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, c.key[:16]).CryptBlocks(ciphertext, padded)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// verifySignature 恒时比较签名
func (c *WXBizMsgCrypt) verifySignature(msgSignature, timestamp, nonce, encryptMsg string) error {
	want := c.signature(timestamp, nonce, encryptMsg)
	if !constantTimeEqual(want, msgSignature) {
		return ErrSignMismatch
	}
	return nil
}

// signature SHA1(sort([encryptMsg, token, timestamp, nonce]) 拼接) —— 字典序（SORT_STRING 语义）
func (c *WXBizMsgCrypt) signature(timestamp, nonce, encryptMsg string) string {
	return signatureSHA1(c.token, timestamp, nonce, encryptMsg)
}

func signatureSHA1(token, timestamp, nonce, encryptMsg string) string {
	arr := []string{encryptMsg, token, timestamp, nonce}
	sort.Strings(arr)
	h := sha1.Sum([]byte(strings.Join(arr, "")))
	return hex.EncodeToString(h[:])
}

// extractEncrypt 从 <xml><Encrypt>..</Encrypt></xml> 提取密文（CDATA 包裹剥除）
func extractEncrypt(xmlBody string) (string, error) {
	open := "<Encrypt>"
	if i := strings.Index(xmlBody, open); i >= 0 {
		rest := xmlBody[i+len(open):]
		if j := strings.Index(rest, "</Encrypt>"); j >= 0 {
			val := strings.TrimSpace(rest[:j])
			val = strings.TrimPrefix(val, "<![CDATA[")
			val = strings.TrimSuffix(val, "]]>")
			return val, nil
		}
	}
	return "", ErrNoEncryptNode
}

// pkcs7Pad PKCS7 填充（微信 32 字节块；整块时补满一块）
func pkcs7Pad(data []byte, blockSize int) []byte {
	amount := blockSize - len(data)%blockSize
	if amount == 0 {
		amount = blockSize
	}
	pad := make([]byte, amount)
	for i := range pad {
		pad[i] = byte(amount)
	}
	return append(data, pad...)
}

// pkcs7Unpad 去 PKCS7（pad 越界视为 0——对齐 PHP 容错语义）
func pkcs7Unpad(data []byte, blockSize int) []byte {
	if len(data) == 0 {
		return data
	}
	pad := int(data[len(data)-1])
	if pad < 1 || pad > blockSize {
		pad = 0
	}
	return data[:len(data)-pad]
}

// constantTimeEqual 恒时字符串比较
func constantTimeEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := 0; i < len(a); i++ {
		v |= a[i] ^ b[i]
	}
	return v == 0
}
