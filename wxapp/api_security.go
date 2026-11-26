package wxapp

import (
	"bytes"
	"context"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// APISecurityConfig enables the MP API security protocol for explicitly supported endpoints.
// Keys and certificates are copied and parsed by WithAPISecurity. Certificate map keys
// are platform-assigned IDs shown in MP, NOT X.509 certificate serial numbers.
// Only AES256_GCM and RSAwithSHA256 (RSA-PSS/SHA256, salt length 32) are supported.
type APISecurityConfig struct {
	EncryptionAlgorithm  string
	SignatureAlgorithm   string
	SymmetricKey         []byte
	SymmetricKeySerial   string
	PrivateKeyPEM        []byte
	PlatformCertificates map[string][]byte
	RequestTimeout       time.Duration // zero: 8 seconds
	MaxClockSkew         time.Duration // zero: 5 minutes, checked in both directions
	MaxResponseBytes     int64         // zero: 1 MiB, enforced before decoding or verification
}

// SecurityError reports a fixed safe failure category without exposing credentials,
// payloads, URLs, key material, or underlying transport/cryptographic errors.
// errors.Is supports context cancellation and deadline failures only.
type SecurityError struct {
	Op         string
	contextErr error
}

func (e *SecurityError) Error() string { return "wxapp: API security " + e.Op + " failed" }
func (e *SecurityError) Unwrap() error { return e.contextErr }
func securityFailure(op string) error  { return &SecurityError{Op: op} }

type apiSecurity struct {
	appID         string
	key           []byte
	serial        string
	privateKey    *rsa.PrivateKey
	certificates  map[string]*x509.Certificate
	timeout, skew time.Duration
	maxBody       int64
}

// SecureWxAppService opts into the API security protocol for GetPhoneNumber and
// GetUserRiskRank. All other methods are promoted from the legacy service and
// retain their original protocol. The embedded service is a copy made at opt-in.
// Publish a new WithAPISecurity result for rotation instead of mutating fields.
type SecureWxAppService struct {
	*WxAppService
	apiSecurity *apiSecurity
}

// WithAPISecurity returns a new service with an immutable security snapshot. The
// original service is unchanged. Rotate keys/certificates by constructing another
// copy; in-flight requests retain the old snapshot. Set ordinary service fields
// before copying and do not mutate service fields concurrently with requests.
// The opt-in currently covers GetPhoneNumber and GetUserRiskRank only; login,
// access-token retrieval and other endpoints retain their existing plain protocol.
func (s *WxAppService) WithAPISecurity(c APISecurityConfig) (*SecureWxAppService, error) {
	if s == nil || s.AppID == "" || strings.ContainsAny(s.AppID, "\r\n|") || c.EncryptionAlgorithm != "AES256_GCM" || c.SignatureAlgorithm != "RSAwithSHA256" || len(c.SymmetricKey) != 32 || c.SymmetricKeySerial == "" || strings.ContainsAny(c.SymmetricKeySerial, "\r\n|") || len(c.PlatformCertificates) == 0 {
		return nil, securityFailure("config")
	}
	key, err := parseAPIRSAKey(c.PrivateKeyPEM)
	if err != nil {
		return nil, err
	}
	if c.RequestTimeout < 0 || c.MaxClockSkew < 0 || c.MaxResponseBytes < 0 || c.MaxResponseBytes > 64<<20 {
		return nil, securityFailure("config")
	}
	a := &apiSecurity{appID: s.AppID, key: bytes.Clone(c.SymmetricKey), serial: c.SymmetricKeySerial, privateKey: key, certificates: make(map[string]*x509.Certificate), timeout: c.RequestTimeout, skew: c.MaxClockSkew, maxBody: c.MaxResponseBytes}
	if a.timeout == 0 {
		a.timeout = 8 * time.Second
	}
	if a.skew == 0 {
		a.skew = 5 * time.Minute
	}
	if a.maxBody == 0 {
		a.maxBody = 1 << 20
	}
	for id, data := range c.PlatformCertificates {
		if id == "" || strings.ContainsAny(id, "\r\n|") {
			return nil, securityFailure("config")
		}
		block, rest := pem.Decode(data)
		if block == nil || block.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 {
			return nil, securityFailure("config")
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, securityFailure("config")
		}
		now := time.Now()
		if now.Before(cert.NotBefore) || now.After(cert.NotAfter) {
			return nil, securityFailure("config")
		}
		pub, ok := cert.PublicKey.(*rsa.PublicKey)
		if !ok || pub.N.BitLen() < 2048 || pub.N.BitLen() > 8192 || (cert.KeyUsage != 0 && cert.KeyUsage&x509.KeyUsageDigitalSignature == 0) {
			return nil, securityFailure("config")
		}
		a.certificates[id] = cert
	}
	result := *s
	return &SecureWxAppService{WxAppService: &result, apiSecurity: a}, nil
}

func parseAPIRSAKey(data []byte) (*rsa.PrivateKey, error) {
	block, rest := pem.Decode(data)
	if block == nil || len(bytes.TrimSpace(rest)) != 0 {
		return nil, securityFailure("config")
	}
	var key *rsa.PrivateKey
	switch block.Type {
	case "PRIVATE KEY":
		parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, securityFailure("config")
		}
		key, _ = parsed.(*rsa.PrivateKey)
	case "RSA PRIVATE KEY":
		key, _ = x509.ParsePKCS1PrivateKey(block.Bytes)
	}
	if key == nil || key.N.BitLen() < 2048 || key.N.BitLen() > 8192 || key.Validate() != nil {
		return nil, securityFailure("config")
	}
	return key, nil
}

type apiCiphertext struct {
	IV      string `json:"iv"`
	Data    string `json:"data"`
	AuthTag string `json:"authtag"`
}

func apiAAD(target, appID, timestamp, serial string) []byte {
	return []byte(strings.Join([]string{target, appID, timestamp, serial}, "|"))
}
func apiSignatureData(target, appID, timestamp string, body []byte) []byte {
	return []byte(target + "\n" + appID + "\n" + timestamp + "\n" + string(body))
}
func apiGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, securityFailure("encrypt")
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, securityFailure("encrypt")
	}
	return gcm, nil
}
func apiEncrypt(key, iv, plain, aad []byte) ([]byte, error) {
	gcm, err := apiGCM(key)
	if err != nil {
		return nil, err
	}
	if len(iv) != gcm.NonceSize() {
		return nil, securityFailure("encrypt")
	}
	sealed := gcm.Seal(nil, iv, plain, aad)
	n := len(sealed) - gcm.Overhead()
	raw, err := json.Marshal(apiCiphertext{base64.StdEncoding.EncodeToString(iv), base64.StdEncoding.EncodeToString(sealed[:n]), base64.StdEncoding.EncodeToString(sealed[n:])})
	if err != nil {
		return nil, securityFailure("encrypt")
	}
	return raw, nil
}
func apiDecrypt(key, raw, aad []byte) ([]byte, error) {
	var e apiCiphertext
	if json.Unmarshal(raw, &e) != nil {
		return nil, securityFailure("decrypt")
	}
	iv, e1 := base64.StdEncoding.Strict().DecodeString(e.IV)
	data, e2 := base64.StdEncoding.Strict().DecodeString(e.Data)
	tag, e3 := base64.StdEncoding.Strict().DecodeString(e.AuthTag)
	if e1 != nil || e2 != nil || e3 != nil || len(iv) != 12 || len(tag) != 16 {
		return nil, securityFailure("decrypt")
	}
	gcm, err := apiGCM(key)
	if err != nil {
		return nil, securityFailure("decrypt")
	}
	plain, err := gcm.Open(nil, iv, append(data, tag...), aad)
	if err != nil {
		return nil, securityFailure("decrypt")
	}
	return plain, nil
}
func apiSign(key *rsa.PrivateKey, data []byte) (string, error) {
	hash := sha256.Sum256(data)
	sig, err := rsa.SignPSS(rand.Reader, key, crypto.SHA256, hash[:], &rsa.PSSOptions{SaltLength: 32, Hash: crypto.SHA256})
	if err != nil {
		return "", securityFailure("sign")
	}
	return base64.StdEncoding.EncodeToString(sig), nil
}
func (a *apiSecurity) verify(headers http.Header, target string, raw []byte, now time.Time) (string, error) {
	appID := headers.Get("Wechatmp-AppId")
	ts := headers.Get("Wechatmp-TimeStamp")
	stamp, err := strconv.ParseInt(ts, 10, 64)
	if appID != a.appID || err != nil || strconv.FormatInt(stamp, 10) != ts || stamp <= 0 {
		return "", securityFailure("verify")
	}
	delta := now.Sub(time.Unix(stamp, 0))
	if delta > a.skew || delta < -a.skew {
		return "", securityFailure("verify")
	}
	hash := sha256.Sum256(apiSignatureData(target, appID, ts, raw))
	pairs := [][2]string{{"Wechatmp-Serial", "Wechatmp-Signature"}, {"Wechatmp-Serial-Deprecated", "Wechatmp-Signature-Deprecated"}}
	for _, pair := range pairs {
		cert := a.certificates[headers.Get(pair[0])]
		if cert == nil || now.Before(cert.NotBefore) || now.After(cert.NotAfter) {
			continue
		}
		sig, err := base64.StdEncoding.Strict().DecodeString(headers.Get(pair[1]))
		if err != nil {
			continue
		}
		// Response PSS salt lengths are auto-detected, as required by the platform.
		if rsa.VerifyPSS(cert.PublicKey.(*rsa.PublicKey), crypto.SHA256, hash[:], sig, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthAuto, Hash: crypto.SHA256}) == nil {
			return ts, nil
		}
	}
	return "", securityFailure("verify")
}

func (s *SecureWxAppService) requestAPISecurity(ctx context.Context, ep Endpoint, query map[string]string, raw []byte) (map[string]any, error) {
	if s == nil || s.WxAppService == nil || s.apiSecurity == nil {
		return nil, securityFailure("config")
	}
	a := s.apiSecurity // immutable request snapshot, including bound appid and key generation
	if s.AppID != a.appID {
		return nil, securityFailure("config")
	}
	target, u, err := s.apiSecurityTarget(ep.Path)
	if err != nil {
		return nil, err
	}
	var plain map[string]json.RawMessage
	if json.Unmarshal(raw, &plain) != nil || plain == nil {
		return nil, securityFailure("encode")
	}
	for k, v := range query {
		if k != "access_token" {
			if _, exists := plain[k]; exists {
				return nil, securityFailure("encode")
			}
			plain[k], _ = json.Marshal(v)
		}
	}
	for _, k := range []string{"_n", "_appid", "_timestamp", "access_token"} {
		if _, exists := plain[k]; exists {
			return nil, securityFailure("encode")
		}
	}
	nonce := make([]byte, 24)
	iv := make([]byte, 12)
	if _, err = rand.Read(nonce); err != nil {
		return nil, securityFailure("encrypt")
	}
	if _, err = rand.Read(iv); err != nil {
		return nil, securityFailure("encrypt")
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	plain["_n"], _ = json.Marshal(base64.StdEncoding.EncodeToString(nonce))
	plain["_appid"], _ = json.Marshal(a.appID)
	plain["_timestamp"] = json.RawMessage(ts)
	data, err := json.Marshal(plain)
	if err != nil {
		return nil, securityFailure("encode")
	}
	encrypted, err := apiEncrypt(a.key, iv, data, apiAAD(target, a.appID, ts, a.serial))
	if err != nil {
		return nil, err
	}
	sig, err := apiSign(a.privateKey, apiSignatureData(target, a.appID, ts, encrypted))
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()
	tokenQuery := url.Values{}
	if token, ok := query["access_token"]; ok {
		tokenQuery.Set("access_token", token)
	}
	if len(tokenQuery) > 0 {
		u.RawQuery = tokenQuery.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, ep.Method, u.String(), bytes.NewReader(encrypted))
	if err != nil {
		return nil, securityFailure("request")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Wechatmp-AppId", a.appID)
	req.Header.Set("Wechatmp-TimeStamp", ts)
	req.Header.Set("Wechatmp-Signature", sig)
	client := s.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	safeClient := *client
	safeClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := safeClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, &SecurityError{Op: "http", contextErr: ctx.Err()}
		}
		return nil, securityFailure("http")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, securityFailure("http")
	}
	received, err := io.ReadAll(io.LimitReader(response.Body, a.maxBody+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, &SecurityError{Op: "http", contextErr: ctx.Err()}
		}
		return nil, securityFailure("http")
	}
	if int64(len(received)) > a.maxBody {
		return nil, securityFailure("size")
	}
	responseTS, err := a.verify(response.Header, target, received, time.Now())
	if err != nil {
		return nil, err
	}
	decrypted, err := apiDecrypt(a.key, received, apiAAD(target, a.appID, responseTS, a.serial))
	if err != nil {
		return nil, err
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(decrypted, &envelope) != nil {
		return nil, securityFailure("decode")
	}
	var appID, random string
	if json.Unmarshal(envelope["_appid"], &appID) != nil || appID != a.appID || !bytes.Equal(bytes.TrimSpace(envelope["_timestamp"]), []byte(responseTS)) || json.Unmarshal(envelope["_n"], &random) != nil || random == "" {
		return nil, securityFailure("binding")
	}
	delete(envelope, "_appid")
	delete(envelope, "_timestamp")
	delete(envelope, "_n")
	clean, err := json.Marshal(envelope)
	if err != nil {
		return nil, securityFailure("decode")
	}
	var decoded map[string]any
	if json.Unmarshal(clean, &decoded) != nil {
		return nil, securityFailure("decode")
	}
	if code, ok := decoded["errcode"].(float64); ok && code != 0 {
		// Even authenticated upstream errors can reflect submitted credentials.
		// Keep only the numeric business code; do not return reflected payloads.
		return nil, &BizError{Errcode: int(code), Errmsg: "API request rejected"}
	}
	return decoded, nil
}

// GetAccessToken preserves the legacy plain token protocol while making
// failures safe for callers of enabled endpoints. The client copy also prevents
// a redirect from forwarding application credentials to a different endpoint.
func (s *SecureWxAppService) GetAccessToken(ctx context.Context) (string, error) {
	if s == nil || s.WxAppService == nil || s.apiSecurity == nil {
		return "", securityFailure("config")
	}
	if s.AppID != s.apiSecurity.appID {
		return "", securityFailure("config")
	}
	if _, _, err := s.apiSecurityTarget(epToken.Path); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, s.apiSecurity.timeout)
	defer cancel()
	service := *s.WxAppService
	client := s.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	safeClient := *client
	safeClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	service.HTTPClient = &safeClient
	token, err := service.GetAccessToken(ctx)
	if err == nil {
		return token, nil
	}
	if ctx.Err() != nil {
		return "", &SecurityError{Op: "token", contextErr: ctx.Err()}
	}
	if errors.Is(err, context.Canceled) {
		return "", &SecurityError{Op: "token", contextErr: context.Canceled}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "", &SecurityError{Op: "token", contextErr: context.DeadlineExceeded}
	}
	var biz *BizError
	if errors.As(err, &biz) {
		return "", &BizError{Errcode: biz.Errcode, Errmsg: "API request rejected"}
	}
	return "", securityFailure("token")
}
func (s *SecureWxAppService) apiSecurityTarget(path string) (string, *url.URL, error) {
	base := s.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	target := base + path
	u, err := url.Parse(target)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return "", nil, securityFailure("request")
	}
	if u.Scheme == "http" {
		ip := net.ParseIP(u.Hostname())
		if u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return "", nil, securityFailure("request")
		}
	}
	return target, u, nil
}

// GetPhoneNumber exchanges a phone code through authenticated encryption.
func (s *SecureWxAppService) GetPhoneNumber(ctx context.Context, code string) (string, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(map[string]string{"code": code})
	if err != nil {
		return "", securityFailure("encode")
	}
	decoded, err := s.requestAPISecurity(ctx, epGetUserPhone, map[string]string{"access_token": token}, raw)
	if err != nil {
		return "", err
	}
	info, _ := decoded["phone_info"].(map[string]any)
	phone, _ := info["phoneNumber"].(string)
	if phone == "" {
		return "", securityFailure("decode")
	}
	return phone, nil
}

// GetUserRiskRank calls the explicitly supported encrypted risk-rank endpoint.
func (s *SecureWxAppService) GetUserRiskRank(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, securityFailure("encode")
	}
	return s.requestAPISecurity(ctx, epUserRiskRank, map[string]string{"access_token": token}, raw)
}
