package wxpay

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

// V2Credentials V2 轨凭证（全构造注入）：APIKey 为商户 API 密钥（V2 签名用）；
// TLSCertPEM/TLSKeyPEM 为商户证书（p12 由调用方转 PEM），仅需证书端点（红包等）走双向认证。
type V2Credentials struct {
	MchID      string
	AppID      string
	APIKey     string
	TLSCertPEM []byte
	TLSKeyPEM  []byte
}

// V2Client 微信支付 V2 客户端（XML 报文 + API 密钥签名 + 商户证书双向认证）
type V2Client struct {
	Creds      V2Credentials
	HTTPClient *http.Client // 需证书端点自动切换内置 TLS Client
	BaseURL    string       // 默认 https://api.mch.weixin.qq.com
}

const defaultV2API = "https://api.mch.weixin.qq.com"

// NewV2Client 构造
func NewV2Client(creds V2Credentials) *V2Client {
	return &V2Client{Creds: creds}
}

// V2BizError V2 错误形态：return_code=FAIL 或 result_code=FAIL（与 V3 BizError 形态不同）
type V2BizError struct {
	ReturnCode string
	ResultCode string
	ErrCode    string
	ErrCodeDes string
}

func (e *V2BizError) Error() string {
	return fmt.Sprintf("wxpay-v2: 业务错误 return=%s result=%s err_code=%s des=%s",
		e.ReturnCode, e.ResultCode, e.ErrCode, e.ErrCodeDes)
}

// signV2 V2 签名：非空参数 k=v 字典序 & 拼接 + &key=APIKey，MD5（默认）或 HMAC-SHA256，hex 大写
func signV2(params map[string]string, key, signType string) string {
	keys := make([]string, 0, len(params))
	for k, v := range params {
		if v == "" || k == "sign" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, k+"="+params[k])
	}
	raw := strings.Join(pairs, "&") + "&key=" + key
	if strings.ToUpper(signType) == "HMAC-SHA256" {
		mac := hmac.New(sha256.New, []byte(key))
		mac.Write([]byte(raw))
		return strings.ToUpper(hex.EncodeToString(mac.Sum(nil)))
	}
	sum := md5.Sum([]byte(raw))
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

// buildV2XML 组 V2 XML 报文：<xml><k>v</k>…</xml>（值 XML 转义）
func buildV2XML(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("<xml>")
	for _, k := range keys {
		b.WriteString("<" + k + ">")
		xml.EscapeText(&b, []byte(params[k]))
		b.WriteString("</" + k + ">")
	}
	b.WriteString("</xml>")
	return b.String()
}

// parseV2XML 解析应答 XML 为叶子 map
func parseV2XML(body string) (map[string]string, error) {
	dec := xml.NewDecoder(strings.NewReader(body))
	fields := map[string]string{}
	var key string
	var depth int
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if depth == 2 {
				key = t.Name.Local
			}
		case xml.CharData:
			if depth == 2 && key != "" {
				fields[key] = string(t)
			}
		case xml.EndElement:
			if depth == 2 {
				key = ""
			}
			depth--
		}
	}
	if len(fields) == 0 {
		return nil, &InfraError{Op: "decode", Err: fmt.Errorf("V2 应答非 XML: %s", body)}
	}
	return fields, nil
}

// doV2 V2 统一分发：注入 appid/mch_id/nonce_str/sign、组 XML、按需证书双向认证、
// 解析应答、return_code/result_code FAIL → V2BizError。
func (c *V2Client) doV2(ctx context.Context, path string, params map[string]string, needCert bool) (map[string]string, error) {
	full := map[string]string{}
	for k, v := range params {
		full[k] = v
	}
	full["appid"] = c.Creds.AppID
	full["mch_id"] = c.Creds.MchID
	full["nonce_str"] = randomNonce()
	full["sign"] = signV2(full, c.Creds.APIKey, full["sign_type"])

	body := buildV2XML(full)
	base := c.BaseURL
	if base == "" {
		base = defaultV2API
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+path, strings.NewReader(body))
	if err != nil {
		return nil, &InfraError{Op: "http", Err: err}
	}
	req.Header.Set("Content-Type", "text/xml")

	client := c.HTTPClient
	if needCert {
		if len(c.Creds.TLSCertPEM) == 0 || len(c.Creds.TLSKeyPEM) == 0 {
			return nil, &InfraError{Op: "http", Err: fmt.Errorf("该端点需商户证书（注入 TLSCertPEM/TLSKeyPEM）")}
		}
		cert, err := tls.X509KeyPair(c.Creds.TLSCertPEM, c.Creds.TLSKeyPEM)
		if err != nil {
			return nil, &InfraError{Op: "http", Err: fmt.Errorf("商户证书加载失败: %w", err)}
		}
		tlsCfg := &tls.Config{Certificates: []tls.Certificate{cert}}
		// 继承注入 client 的 RootCAs（测试桩 CA / 企业代理场景）
		if base, ok := client.Transport.(*http.Transport); ok && base.TLSClientConfig != nil && base.TLSClientConfig.RootCAs != nil {
			tlsCfg.RootCAs = base.TLSClientConfig.RootCAs
		}
		client = &http.Client{Transport: &http.Transport{TLSClientConfig: tlsCfg}}
	}
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, &InfraError{Op: "http", Err: err}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &InfraError{Op: "http", Err: err}
	}
	fields, err := parseV2XML(string(raw))
	if err != nil {
		return nil, err
	}
	if fields["return_code"] == "FAIL" || (fields["result_code"] == "FAIL" && fields["return_code"] == "SUCCESS") {
		return fields, &V2BizError{
			ReturnCode: fields["return_code"], ResultCode: fields["result_code"],
			ErrCode: fields["err_code"], ErrCodeDes: fields["err_code_des"],
		}
	}
	return fields, nil
}

var _ = bytes.MinRead
