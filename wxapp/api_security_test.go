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
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"maps"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shyun-cc/shw-gf-lib/internal/testredis"
)

// TestAPISecurityAESVector checks the wire format against a synthetic vector
// generated independently with the standard library AES-GCM primitives.
func TestAPISecurityAESVector(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, 32)
	iv := []byte("test-nonce01")
	plain := []byte(`{"appid":"test-app","openid":"test-user"}`)
	aad := []byte("https://api.example.com/wxa/example|test-app|1700000000|test-key-id")
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	sealed := gcm.Seal(nil, iv, plain, aad)
	tagStart := len(sealed) - 16
	expectedFields := map[string]string{
		"iv":      base64.StdEncoding.EncodeToString(iv),
		"data":    base64.StdEncoding.EncodeToString(sealed[:tagStart]),
		"authtag": base64.StdEncoding.EncodeToString(sealed[tagStart:]),
	}
	got, err := apiEncrypt(key, iv, plain, aad)
	if err != nil {
		t.Fatal(err)
	}
	var gotFields map[string]string
	if err := json.Unmarshal(got, &gotFields); err != nil {
		t.Fatal(err)
	}
	expected, err := json.Marshal(expectedFields)
	if err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(gotFields, expectedFields) {
		t.Fatal("AES-GCM wire format mismatch")
	}
	decrypted, err := apiDecrypt(key, expected, aad)
	if err != nil || !bytes.Equal(decrypted, plain) {
		t.Fatal("AES-GCM vector decrypt mismatch")
	}
	if _, err = apiDecrypt(key, expected, append(aad, 'x')); err == nil {
		t.Fatal("tampered AAD accepted")
	}
}

type apiSecurityFixture struct {
	key    *rsa.PrivateKey
	config APISecurityConfig
}

func newAPISecurityFixture(t *testing.T) apiSecurityFixture {
	t.Helper()
	private, _, err := GenerateRSAKeyPair(2048)
	if err != nil {
		t.Fatal(err)
	}
	key, err := parseAPIRSAKey(private)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: "local MP fixture"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return apiSecurityFixture{key, APISecurityConfig{EncryptionAlgorithm: "AES256_GCM", SignatureAlgorithm: "RSAwithSHA256", SymmetricKey: bytes.Repeat([]byte{0x3a}, 32), SymmetricKeySerial: "symmetric-platform-id", PrivateKeyPEM: private, PlatformCertificates: map[string][]byte{"platform-id-not-x509-7": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}}}
}

// These fixture helpers directly use the standard library primitives rather
// than the production encrypt/sign functions, so public-call tests exercise
// canonicalization, IV/tag boundaries, and PSS salt interoperability.
func fixtureAPISeal(t *testing.T, key, plain, aad []byte) []byte {
	t.Helper()
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	iv := make([]byte, 12)
	if _, err = rand.Read(iv); err != nil {
		t.Fatal(err)
	}
	sealed := gcm.Seal(nil, iv, plain, aad)
	out, err := json.Marshal(map[string]string{"iv": base64.StdEncoding.EncodeToString(iv), "data": base64.StdEncoding.EncodeToString(sealed[:len(sealed)-16]), "authtag": base64.StdEncoding.EncodeToString(sealed[len(sealed)-16:])})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func fixtureAPISign(t *testing.T, key *rsa.PrivateKey, target, appID, stamp string, raw []byte) string {
	t.Helper()
	hash := sha256.Sum256([]byte(target + "\n" + appID + "\n" + stamp + "\n" + string(raw)))
	sig, err := rsa.SignPSS(rand.Reader, key, crypto.SHA256, hash[:], &rsa.PSSOptions{SaltLength: 32, Hash: crypto.SHA256})
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(sig)
}
func fixtureAPIResponse(t *testing.T, f apiSecurityFixture, target, appID, stamp string, body map[string]any) (http.Header, []byte) {
	t.Helper()
	n, err := strconv.ParseInt(stamp, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	body["_appid"] = appID
	body["_timestamp"] = n
	body["_n"] = "response-unique-nonce"
	plain, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	raw := fixtureAPISeal(t, f.config.SymmetricKey, plain, []byte(target+"|"+appID+"|"+stamp+"|"+f.config.SymmetricKeySerial))
	headers := make(http.Header)
	headers.Set("Wechatmp-AppId", appID)
	headers.Set("Wechatmp-TimeStamp", stamp)
	headers.Set("Wechatmp-Serial", "platform-id-not-x509-7")
	headers.Set("Wechatmp-Signature", fixtureAPISign(t, f.key, target, appID, stamp, raw))
	return headers, raw
}
func assertAPISecurityRequest(t *testing.T, f apiSecurityFixture, r *http.Request, raw []byte, target string) map[string]any {
	t.Helper()
	appID := r.Header.Get("Wechatmp-AppId")
	stamp := r.Header.Get("Wechatmp-TimeStamp")
	sig, err := base64.StdEncoding.DecodeString(r.Header.Get("Wechatmp-Signature"))
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte(target + "\n" + appID + "\n" + stamp + "\n" + string(raw)))
	if rsa.VerifyPSS(&f.key.PublicKey, crypto.SHA256, hash[:], sig, &rsa.PSSOptions{SaltLength: 32, Hash: crypto.SHA256}) != nil {
		t.Fatal("request signature or salt length invalid")
	}
	var envelope map[string]string
	if json.Unmarshal(raw, &envelope) != nil {
		t.Fatal("invalid request envelope")
	}
	iv, _ := base64.StdEncoding.DecodeString(envelope["iv"])
	data, _ := base64.StdEncoding.DecodeString(envelope["data"])
	tag, _ := base64.StdEncoding.DecodeString(envelope["authtag"])
	block, _ := aes.NewCipher(f.config.SymmetricKey)
	gcm, _ := cipher.NewGCM(block)
	plain, err := gcm.Open(nil, iv, append(data, tag...), []byte(target+"|"+appID+"|"+stamp+"|"+f.config.SymmetricKeySerial))
	if err != nil {
		t.Fatal("request authentication failed")
	}
	var body map[string]any
	if json.Unmarshal(plain, &body) != nil {
		t.Fatal("invalid plaintext")
	}
	if body["_appid"] != appID || body["_timestamp"] != float64(mustParseAPITimestamp(t, stamp)) || body["_n"] == "" {
		t.Fatal("request security binding missing")
	}
	if _, exists := body["access_token"]; exists {
		t.Fatal("token entered plaintext")
	}
	if r.Header.Get("Wechatmp-Serial") != "" {
		t.Fatal("invented request serial header")
	}
	return body
}
func mustParseAPITimestamp(t *testing.T, stamp string) int64 {
	t.Helper()
	n, err := strconv.ParseInt(stamp, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestAPISecurityPublicEndpoints(t *testing.T) {
	mr := testredis.Start(t)
	mr.Set(accessTokenKeyPrefix+"wx-secure", "secret-token")
	f := newAPISecurityFixture(t)
	var targetBase string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		target := targetBase + r.URL.Path
		if r.URL.RawQuery != "access_token=secret-token" {
			t.Error("unexpected token/query transport")
		}
		body := assertAPISecurityRequest(t, f, r, raw, target)
		reply := map[string]any{"errcode": 0, "risk_rank": 2}
		if r.URL.Path == epGetUserPhone.Path {
			if body["code"] != "private-code" {
				t.Error("phone code lost")
			}
			reply["phone_info"] = map[string]any{"phoneNumber": "10800000000"}
		} else if body["scene"] != float64(1) {
			t.Error("risk parameters lost")
		}
		headers, response := fixtureAPIResponse(t, f, target, "wx-secure", strconv.FormatInt(time.Now().Unix(), 10), reply)
		for k, v := range headers {
			w.Header()[k] = v
		}
		_, _ = w.Write(response)
	}))
	defer server.Close()
	targetBase = server.URL
	original := NewWxAppService("wx-secure", "secret")
	original.BaseURL = server.URL
	secure, err := original.WithAPISecurity(f.config)
	if err != nil {
		t.Fatal(err)
	}
	if secure.WxAppService == original {
		t.Fatal("WithAPISecurity mutated source")
	}
	phone, err := secure.GetPhoneNumber(context.Background(), "private-code")
	if err != nil || phone != "10800000000" {
		t.Fatalf("phone call failed: %v", err)
	}
	risk, err := secure.GetUserRiskRank(context.Background(), map[string]any{"scene": 1})
	if err != nil || risk["risk_rank"] != float64(2) {
		t.Fatalf("risk call failed: %v", err)
	}
	for _, k := range []string{"_appid", "_timestamp", "_n"} {
		if _, ok := risk[k]; ok {
			t.Error("security envelope leaked into result")
		}
	}
}

func TestAPISecurityRejectsUntrustedResponses(t *testing.T) {
	mr := testredis.Start(t)
	mr.Set(accessTokenKeyPrefix+"wx-secure", "secret-token")
	f := newAPISecurityFixture(t)
	cases := []string{"headerless", "tamper", "wrong-app", "stale", "future", "wrong-id", "missing-signature", "plaintext", "bad-iv", "bad-tag", "bad-json", "wrong-binding", "wrong-time-binding", "missing-nonce", "http-status", "oversize", "redirect", "signed-biz-error"}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				target := server.URL + r.URL.Path
				stamp := strconv.FormatInt(time.Now().Unix(), 10)
				appID := "wx-secure"
				if name == "stale" {
					stamp = strconv.FormatInt(time.Now().Add(-6*time.Minute).Unix(), 10)
				}
				if name == "future" {
					stamp = strconv.FormatInt(time.Now().Add(6*time.Minute).Unix(), 10)
				}
				if name == "wrong-app" {
					appID = "other-app"
				}
				reply := map[string]any{"errcode": 0, "risk_rank": 1}
				if name == "signed-biz-error" {
					reply["errcode"] = 40001
					reply["errmsg"] = "secret-token private-code secret-upstream"
				}
				headers, raw := fixtureAPIResponse(t, f, target, appID, stamp, reply)
				switch name {
				case "headerless":
					headers = make(http.Header)
				case "tamper":
					raw = append(raw, ' ')
				case "wrong-id":
					headers.Set("Wechatmp-Serial", "7") // x509 serial is not a platform ID
				case "missing-signature":
					headers.Del("Wechatmp-Signature")
				case "plaintext":
					raw = []byte(`{"errcode":0,"risk_rank":1}`)
				case "bad-iv", "bad-tag":
					var e map[string]string
					_ = json.Unmarshal(raw, &e)
					if name == "bad-iv" {
						e["iv"] = "AA=="
					} else {
						e["authtag"] = "AA=="
					}
					raw, _ = json.Marshal(e)
				case "bad-json":
					raw = []byte(`not JSON`)
				case "wrong-binding", "wrong-time-binding", "missing-nonce":
					binding := map[string]any{"_appid": appID, "_timestamp": mustParseAPITimestamp(t, stamp), "_n": "nonce", "risk_rank": 1}
					if name == "wrong-binding" {
						binding["_appid"] = "other"
					}
					if name == "wrong-time-binding" {
						binding["_timestamp"] = 1
					}
					if name == "missing-nonce" {
						delete(binding, "_n")
					}
					plain, _ := json.Marshal(binding)
					raw = fixtureAPISeal(t, f.config.SymmetricKey, plain, apiAAD(target, appID, stamp, f.config.SymmetricKeySerial))
				case "http-status":
					w.WriteHeader(http.StatusBadGateway)
				case "oversize":
					raw = bytes.Repeat([]byte("x"), 2048)
				case "redirect":
					w.Header().Set("Location", "https://should-never-be-followed.invalid/?access_token=secret-token")
					w.WriteHeader(http.StatusFound)
				}
				// Preserve deliberately invalid signatures for tamper/missing/headerless cases.
				if name != "tamper" && name != "missing-signature" && name != "headerless" {
					headers.Set("Wechatmp-Signature", fixtureAPISign(t, f.key, target, appID, stamp, raw))
				}
				for k, v := range headers {
					w.Header()[k] = v
				}
				_, _ = w.Write(raw)
			}))
			defer server.Close()
			original := NewWxAppService("wx-secure", "secret")
			original.BaseURL = server.URL
			config := f.config
			if name == "oversize" {
				config.MaxResponseBytes = 1024
			}
			secure, err := original.WithAPISecurity(config)
			if err != nil {
				t.Fatal(err)
			}
			_, err = secure.GetUserRiskRank(context.Background(), map[string]any{"scene": 1})
			if name == "signed-biz-error" {
				var biz *BizError
				if !errors.As(err, &biz) || biz.Errcode != 40001 {
					t.Fatal("authenticated business error lost")
				}
				if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "private-code") {
					t.Fatal("business error leaked reflected data")
				}
				return
			}
			var failure *SecurityError
			if !errors.As(err, &failure) {
				t.Fatalf("expected SecurityError, got %T", err)
			}
			for _, secret := range []string{"secret-token", "private-code", server.URL, "PRIVATE KEY", "other-app"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatal("error leaked sensitive data")
				}
			}
		})
	}
}

func TestAPISecurityConfigurationSnapshotAndRotation(t *testing.T) {
	f := newAPISecurityFixture(t)
	original := NewWxAppService("wx-one", "secret")
	secure, err := original.WithAPISecurity(f.config)
	if err != nil {
		t.Fatal(err)
	}
	expectedKey := bytes.Clone(secure.apiSecurity.key)
	f.config.SymmetricKey[0] ^= 255
	f.config.PrivateKeyPEM[0] = '!'
	f.config.PlatformCertificates["platform-id-not-x509-7"][0] = '!'
	delete(f.config.PlatformCertificates, "platform-id-not-x509-7")
	if !bytes.Equal(secure.apiSecurity.key, expectedKey) || len(secure.apiSecurity.certificates) != 1 {
		t.Fatal("configuration aliases caller memory")
	}
	second := newAPISecurityFixture(t)
	rotated, err := secure.WithAPISecurity(second.config)
	if err != nil {
		t.Fatal(err)
	}
	target := "https://api.weixin.qq.com/wxa/getuserriskrank"
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	// The old snapshot remains available to in-flight work while callers publish
	// the rotated service. Independent goroutines must not alter either snapshot.
	var wg sync.WaitGroup
	for _, service := range []*SecureWxAppService{secure, rotated} {
		wg.Add(1)
		go func(service *SecureWxAppService) {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				if service.apiSecurity.appID != "wx-one" {
					t.Error("appid snapshot mutated")
				}
				_, err := apiSign(service.apiSecurity.privateKey, apiSignatureData(target, "wx-one", ts, []byte(`{}`)))
				if err != nil {
					t.Error(err)
				}
			}
		}(service)
	}
	wg.Wait()
	headers, response := fixtureAPIResponse(t, second, target, "wx-one", ts, map[string]any{"errcode": 0})
	if _, err := rotated.apiSecurity.verify(headers, target, response, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := secure.apiSecurity.verify(headers, target, response, time.Now()); err == nil {
		t.Fatal("old service accepted untrusted rotated key")
	}
	// Only an explicitly configured deprecated ID/signature pair is trusted.
	headers.Set("Wechatmp-Serial-Deprecated", headers.Get("Wechatmp-Serial"))
	headers.Set("Wechatmp-Signature-Deprecated", headers.Get("Wechatmp-Signature"))
	headers.Set("Wechatmp-Serial", "unknown")
	if _, err := rotated.apiSecurity.verify(headers, target, response, time.Now()); err != nil {
		t.Fatal("trusted deprecated pair rejected")
	}
	headers.Set("Wechatmp-Serial-Deprecated", "7")
	if _, err := rotated.apiSecurity.verify(headers, target, response, time.Now()); err == nil {
		t.Fatal("unknown deprecated certificate accepted")
	}
}

func TestAPISecurityConfigValidation(t *testing.T) {
	f := newAPISecurityFixture(t)
	invalid := []func(*APISecurityConfig){func(c *APISecurityConfig) { c.EncryptionAlgorithm = "SM4_GCM" }, func(c *APISecurityConfig) { c.SignatureAlgorithm = "SM2withSM3" }, func(c *APISecurityConfig) { c.SymmetricKey = make([]byte, 16) }, func(c *APISecurityConfig) { c.SymmetricKeySerial = "" }, func(c *APISecurityConfig) { c.PrivateKeyPEM = []byte("secret-invalid-key") }, func(c *APISecurityConfig) { c.PlatformCertificates = nil }, func(c *APISecurityConfig) {
		c.PlatformCertificates = map[string][]byte{"id": []byte("secret-invalid-certificate")}
	}, func(c *APISecurityConfig) { c.RequestTimeout = -1 }, func(c *APISecurityConfig) { c.MaxClockSkew = -1 }, func(c *APISecurityConfig) { c.MaxResponseBytes = -1 }}
	for i, change := range invalid {
		config := f.config
		change(&config)
		if _, err := NewWxAppService("wx", "secret").WithAPISecurity(config); err == nil {
			t.Errorf("invalid configuration %d accepted", i)
		}
	}
	service, err := NewWxAppService("wx", "secret").WithAPISecurity(f.config)
	if err != nil {
		t.Fatal(err)
	}
	if service.apiSecurity.timeout != 8*time.Second || service.apiSecurity.skew != 5*time.Minute || service.apiSecurity.maxBody != 1<<20 {
		t.Fatal("secure defaults changed")
	}
	service.AppID = "wx-changed"
	if _, err = service.requestAPISecurity(context.Background(), epUserRiskRank, nil, []byte(`{}`)); err == nil {
		t.Fatal("mutable appid bypassed bound snapshot")
	}
}

type apiSecurityRoundTripper func(*http.Request) (*http.Response, error)

func (f apiSecurityRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestAPISecurityCancellationAndSafeTransportError(t *testing.T) {
	f := newAPISecurityFixture(t)
	base := NewWxAppService("wx", "secret")
	base.HTTPClient = &http.Client{Transport: apiSecurityRoundTripper(func(r *http.Request) (*http.Response, error) {
		return nil, errors.New("https://secret-host/?access_token=secret-token")
	})}
	secure, err := base.WithAPISecurity(f.config)
	if err != nil {
		t.Fatal(err)
	}
	_, err = secure.requestAPISecurity(context.Background(), epUserRiskRank, nil, []byte(`{}`))
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatal("transport error escaped sanitization")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = secure.requestAPISecurity(ctx, epUserRiskRank, nil, []byte(`{}`))
	if !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation identity lost")
	}
	config := f.config
	config.RequestTimeout = time.Nanosecond
	secure, err = base.WithAPISecurity(config)
	if err != nil {
		t.Fatal(err)
	}
	_, err = secure.requestAPISecurity(context.Background(), epUserRiskRank, nil, []byte(`{}`))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("timeout identity lost")
	}
}

func TestAPISecurityInFlightRotation(t *testing.T) {
	mr := testredis.Start(t)
	mr.Set(accessTokenKeyPrefix+"wx-rotation", "token")
	oldMaterial := newAPISecurityFixture(t)
	newMaterial := newAPISecurityFixture(t)
	started := make(chan struct{})
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		target := server.URL + r.URL.Path
		signature, _ := base64.StdEncoding.DecodeString(r.Header.Get("Wechatmp-Signature"))
		hash := sha256.Sum256(apiSignatureData(target, "wx-rotation", r.Header.Get("Wechatmp-TimeStamp"), raw))
		material := newMaterial
		if rsa.VerifyPSS(&oldMaterial.key.PublicKey, crypto.SHA256, hash[:], signature, &rsa.PSSOptions{SaltLength: 32, Hash: crypto.SHA256}) == nil {
			material = oldMaterial
			close(started)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			case <-time.After(3 * time.Second):
				t.Error("rotation handler release timed out")
				return
			}
		}
		headers, response := fixtureAPIResponse(t, material, target, "wx-rotation", strconv.FormatInt(time.Now().Unix(), 10), map[string]any{"errcode": 0, "risk_rank": 1})
		for k, v := range headers {
			w.Header()[k] = v
		}
		_, _ = w.Write(response)
	}))
	defer server.Close()
	base := NewWxAppService("wx-rotation", "secret")
	base.BaseURL = server.URL
	oldService, err := base.WithAPISecurity(oldMaterial.config)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, callErr := oldService.GetUserRiskRank(context.Background(), map[string]any{"scene": 1})
		result <- callErr
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("old request never started")
	}
	rotated, err := oldService.WithAPISecurity(newMaterial.config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = rotated.GetUserRiskRank(context.Background(), map[string]any{"scene": 1}); err != nil {
		t.Fatal("rotated request failed", err)
	}
	close(release)
	select {
	case err := <-result:
		if err != nil {
			t.Fatal("in-flight request lost old material", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("in-flight request did not complete")
	}
}

func TestAPISecurityPlainEndpointAndFailureIsolation(t *testing.T) {
	mr := testredis.Start(t)
	mr.Set(accessTokenKeyPrefix+"wx-isolation", "token")
	material := newAPISecurityFixture(t)
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != epCode2Session.Path || r.Header.Get("Wechatmp-Signature") != "" || r.URL.Query().Get("appid") != "wx-isolation" {
			t.Error("unsupported endpoint protocol changed")
		}
		_, _ = w.Write([]byte(`{"openid":"open-id","session_key":"session-key"}`))
	}))
	defer server.Close()
	base := NewWxAppService("wx-isolation", "secret")
	base.BaseURL = server.URL
	service, err := base.WithAPISecurity(material.config)
	if err != nil {
		t.Fatal(err)
	}
	session, err := service.Code2Session(context.Background(), "code")
	if err != nil || session.OpenID != "open-id" {
		t.Fatal("plain login regressed", err)
	}
	// Reserved envelope fields reject before dispatch, never retrying plaintext.
	body := map[string]any{"_appid": "attacker-app", "scene": 1}
	_, err = service.GetUserRiskRank(context.Background(), body)
	var securityErr *SecurityError
	if !errors.As(err, &securityErr) || calls != 1 || body["_appid"] != "attacker-app" {
		t.Fatal("security failure fell back or mutated caller body")
	}
	service.BaseURL = "http://external.example"
	_, err = service.GetUserRiskRank(context.Background(), map[string]any{"scene": 1})
	if !errors.As(err, &securityErr) || calls != 1 {
		t.Fatal("non-loopback HTTP accepted")
	}
}

func TestAPISecurityCertificateValidityAndMultipleTrustedIDs(t *testing.T) {
	f := newAPISecurityFixture(t)
	now := time.Now()
	for _, times := range [][2]time.Time{{now.Add(-2 * time.Hour), now.Add(-time.Hour)}, {now.Add(time.Hour), now.Add(2 * time.Hour)}} {
		template := &x509.Certificate{SerialNumber: big.NewInt(17), NotBefore: times[0], NotAfter: times[1], KeyUsage: x509.KeyUsageDigitalSignature}
		der, err := x509.CreateCertificate(rand.Reader, template, template, &f.key.PublicKey, f.key)
		if err != nil {
			t.Fatal(err)
		}
		config := f.config
		config.PlatformCertificates = map[string][]byte{"invalid-time": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
		if _, err = NewWxAppService("wx", "secret").WithAPISecurity(config); err == nil {
			t.Fatal("certificate outside validity accepted")
		}
	}
	second := newAPISecurityFixture(t)
	config := f.config
	config.PlatformCertificates = map[string][]byte{"platform-id-not-x509-7": f.config.PlatformCertificates["platform-id-not-x509-7"], "next-platform-id": second.config.PlatformCertificates["platform-id-not-x509-7"]}
	service, err := NewWxAppService("wx", "secret").WithAPISecurity(config)
	if err != nil {
		t.Fatal(err)
	}
	target := "https://api.weixin.qq.com/wxa/getuserriskrank"
	stamp := strconv.FormatInt(now.Unix(), 10)
	for i, material := range []apiSecurityFixture{f, second} {
		headers, response := fixtureAPIResponse(t, material, target, "wx", stamp, map[string]any{"errcode": 0})
		if i == 1 {
			headers.Set("Wechatmp-Serial", "next-platform-id")
		}
		if _, err = service.apiSecurity.verify(headers, target, response, now); err != nil {
			t.Fatal("overlapping trusted certificate rejected", err)
		}
	}
}

func TestAPISecurityTokenFailureSanitizedThroughPublicMethods(t *testing.T) {
	testredis.Start(t)
	material := newAPISecurityFixture(t)
	base := NewWxAppService("wx-token-failure", "secret")
	base.HTTPClient = &http.Client{Transport: apiSecurityRoundTripper(func(r *http.Request) (*http.Response, error) {
		return nil, errors.New("https://private.example/?secret=secret-upstream&access_token=secret-token")
	})}
	secure, err := base.WithAPISecurity(material.config)
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range []func() error{func() error { _, err := secure.GetPhoneNumber(context.Background(), "private-code"); return err }, func() error {
		_, err := secure.GetUserRiskRank(context.Background(), map[string]any{"scene": 1})
		return err
	}} {
		err := call()
		var failure *SecurityError
		if !errors.As(err, &failure) {
			t.Fatal("token failure escaped secure public method")
		}
		for _, value := range []string{"secret", "private.example", "private-code", "access_token"} {
			if strings.Contains(err.Error(), value) {
				t.Fatal("token error leaked upstream material")
			}
		}
	}
	// The pre-existing service keeps its error contract, even after producing an
	// opted-in copy. This also guards against accidental mutation of its client.
	_, err = base.GetAccessToken(context.Background())
	var infra *InfraError
	if !errors.As(err, &infra) || !strings.Contains(err.Error(), "secret-upstream") {
		t.Fatal("legacy token behavior changed")
	}
}

func TestAPISecurityZeroClientFailsClosed(t *testing.T) {
	for _, service := range []*SecureWxAppService{nil, {}, {WxAppService: NewWxAppService("wx", "secret")}} {
		if _, err := service.GetPhoneNumber(context.Background(), "code"); err == nil {
			t.Fatal("unconfigured secure client accepted")
		}
		if _, err := service.GetUserRiskRank(context.Background(), map[string]any{}); err == nil {
			t.Fatal("unconfigured risk client accepted")
		}
	}
}
