package wxapp

import (
	"bytes"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
)

func newShareTestSigner(t *testing.T, appID string) (*ShareSigner, *rsa.PublicKey, ShareSignerConfig) {
	t.Helper()
	private, public, err := GenerateRSAKeyPair(2048)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(public)
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	config := ShareSignerConfig{AppID: appID, SignatureAlgorithm: "RSAwithSHA256", PrivateKeyPEM: private}
	signer, err := NewShareSigner(config)
	if err != nil {
		t.Fatal(err)
	}
	return signer, parsed.(*rsa.PublicKey), config
}

// The verifier is deliberately independent of shareSignatureData: it builds the
// official sorted field map and verifies through the standard crypto API.
func verifyShareTestSignature(pub *rsa.PublicKey, appID string, payload *ShareConfig) error {
	fields := map[string]string{"title": payload.Title, "thumbDataHash": payload.ThumbDataHash, "path": payload.Path, "appid": appID}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+base64.StdEncoding.EncodeToString([]byte(fields[key])))
	}
	digest := sha256.Sum256([]byte(strings.Join(parts, "&")))
	sig, err := base64.StdEncoding.Strict().DecodeString(payload.Signature)
	if err != nil {
		return err
	}
	return rsa.VerifyPSS(pub, crypto.SHA256, digest[:], sig, &rsa.PSSOptions{SaltLength: 32, Hash: crypto.SHA256})
}

func TestShareSignatureOfficialCanonicalVector(t *testing.T) {
	// Official guide provides this exact canonical SHA256 independently of the
	// randomized RSA-PSS signature. No private fixture credentials are needed.
	payload := shareSignatureData("wxba6223c06417af7b", "/pages/index/share?input=%E6%B5%8B%E8%AF%95", "12c593c5bd2f0972188eb30e32bee7ad4060be582c2c6826caff89372ca84707", "WXA分享测试")
	digest := sha256.Sum256(payload)
	if hex.EncodeToString(digest[:]) != "242c3d1ed8e95fcd418cfb3257c23773fb8d698cd35cb0998567543348090d5c" {
		t.Fatal("official canonical payload digest mismatch")
	}
	if bytes.HasSuffix(payload, []byte("\n")) {
		t.Fatal("unexpected terminal newline")
	}
}

func TestShareSignerPublicPayloadAndInteroperability(t *testing.T) {
	signer, pub, _ := newShareTestSigner(t, "wx-share")
	input := ShareInput{Title: "分享标题 😀 & + =", Path: "/pages/share/index?z=%e6%b5%8b&a=%2F&a=1+2&empty=", ImageURL: "https://images.example/immutable/image.png?v=2", ImageBytes: []byte("exact-original-image-bytes")}
	result, err := signer.BuildShare(input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Title != input.Title || result.Path != input.Path || result.ImageURL != input.ImageURL {
		t.Fatal("share values were normalized or changed")
	}
	expected := sha256.Sum256(input.ImageBytes)
	if result.ThumbDataHash != hex.EncodeToString(expected[:]) {
		t.Fatal("image hash mismatch")
	}
	if err = verifyShareTestSignature(pub, "wx-share", result); err != nil {
		t.Fatal("public payload signature did not interoperate")
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		t.Fatal("invalid JSON")
	}
	if len(fields) != 5 {
		t.Fatal("share JSON must contain exactly five fields")
	}
	for _, key := range []string{"title", "path", "imageUrl", "thumbDataHash", "signature"} {
		if len(fields[key]) == 0 {
			t.Errorf("missing JSON field %s", key)
		}
	}
	for _, forbidden := range []string{"appid", "privateKey", "PrivateKeyPEM", "SignatureAlgorithm"} {
		if _, ok := fields[forbidden]; ok {
			t.Fatal("configuration entered public JSON")
		}
	}
}

func TestShareSignatureBindsOfficialFields(t *testing.T) {
	signer, pub, _ := newShareTestSigner(t, "wx-share")
	signed, err := signer.BuildShare(ShareInput{Title: "原始标题", Path: "/pages/share?z=1&a=%2f", ImageURL: "https://images.example/image", ImageBytes: []byte("image-A")})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		change func(*ShareConfig)
	}{
		{"title", func(p *ShareConfig) { p.Title = "改过的标题" }},
		{"path", func(p *ShareConfig) { p.Path = "/pages/share?a=%2f&z=1" }},
		{"path-encoding", func(p *ShareConfig) { p.Path = "/pages/share?z=1&a=%2F" }},
		{"hash", func(p *ShareConfig) { p.ThumbDataHash = strings.Repeat("a", 64) }},
		{"image-bytes", func(p *ShareConfig) { p.ThumbDataHash, _ = ShareImageHash([]byte("image-B")) }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			tampered := *signed
			test.change(&tampered)
			if verifyShareTestSignature(pub, "wx-share", &tampered) == nil {
				t.Fatal("modified signed data accepted")
			}
		})
	}
	if verifyShareTestSignature(pub, "wx-other", signed) == nil {
		t.Fatal("signature accepted for another appid")
	}
	// imageUrl is deliberately absent from the official signed fields. A second
	// URL serving identical bytes remains valid; changing the bytes fails above.
	alternateURL := *signed
	alternateURL.ImageURL = "https://cdn.example/same-image"
	if verifyShareTestSignature(pub, "wx-share", &alternateURL) != nil {
		t.Fatal("URL unexpectedly entered canonical signature")
	}
}

func TestShareSignerByteAndPrecomputedHashPaths(t *testing.T) {
	signer, pub, _ := newShareTestSigner(t, "wx-equivalent")
	image := []byte("trusted-upload-output")
	hash, err := ShareImageHash(image)
	if err != nil {
		t.Fatal(err)
	}
	fromBytes, err := signer.BuildShare(ShareInput{Title: "标题", Path: "/pages/detail?x=1", ImageURL: "https://images.example/one", ImageBytes: image})
	if err != nil {
		t.Fatal(err)
	}
	fromHash, err := signer.BuildShareWithHash(ShareHashInput{Title: "标题", Path: "/pages/detail?x=1", ImageURL: "https://images.example/one", ThumbDataHash: hash})
	if err != nil {
		t.Fatal(err)
	}
	if fromBytes.ThumbDataHash != fromHash.ThumbDataHash || fromBytes.Title != fromHash.Title || fromBytes.Path != fromHash.Path || fromBytes.ImageURL != fromHash.ImageURL {
		t.Fatal("byte/hash APIs disagree")
	}
	for _, payload := range []*ShareConfig{fromBytes, fromHash} {
		if verifyShareTestSignature(pub, "wx-equivalent", payload) != nil {
			t.Fatal("byte/hash signature failed")
		}
	}
	// Hash exact bytes rather than an image URL, a decoded image, or a text string.
	empty, err := ShareImageHash(nil)
	if err == nil || empty != "" {
		t.Fatal("empty image accepted")
	}
}

func TestShareSignerInvalidInputsAndSafeErrors(t *testing.T) {
	signer, _, config := newShareTestSigner(t, "wx-validation")
	invalidConfig := []ShareSignerConfig{{AppID: "", SignatureAlgorithm: "RSAwithSHA256", PrivateKeyPEM: config.PrivateKeyPEM}, {AppID: "wx", SignatureAlgorithm: "SM2withSM3", PrivateKeyPEM: config.PrivateKeyPEM}, {AppID: "wx", SignatureAlgorithm: "", PrivateKeyPEM: config.PrivateKeyPEM}, {AppID: "wx", SignatureAlgorithm: "RSAwithSHA256", PrivateKeyPEM: []byte("private-secret-key")}, {AppID: string([]byte{0xff}), SignatureAlgorithm: "RSAwithSHA256", PrivateKeyPEM: config.PrivateKeyPEM}}
	for i, c := range invalidConfig {
		result, err := NewShareSigner(c)
		if err == nil || result != nil {
			t.Errorf("invalid config %d accepted", i)
		}
		assertSafeShareError(t, err)
	}
	valid := ShareHashInput{Title: "private-title", Path: "/pages/private?token=private-token", ImageURL: "https://images.example/private", ThumbDataHash: strings.Repeat("a", 64)}
	invalidInputs := []func(*ShareHashInput){
		func(p *ShareHashInput) { p.Title = "" }, func(p *ShareHashInput) { p.Title = string([]byte{0xff}) }, func(p *ShareHashInput) { p.Path = "pages/index" }, func(p *ShareHashInput) { p.Path = "" }, func(p *ShareHashInput) { p.Path = "/" + string([]byte{0xff}) },
		func(p *ShareHashInput) { p.ImageURL = "" }, func(p *ShareHashInput) { p.ImageURL = "http://images.example/plain" }, func(p *ShareHashInput) { p.ImageURL = "https:///image" }, func(p *ShareHashInput) { p.ImageURL = "https://private-user:private-password@images.example/image" }, func(p *ShareHashInput) { p.ImageURL = "https://images.example/image#private-fragment" }, func(p *ShareHashInput) { p.ImageURL = "https://images.example/image#" },
		func(p *ShareHashInput) { p.ThumbDataHash = strings.Repeat("A", 64) }, func(p *ShareHashInput) { p.ThumbDataHash = strings.Repeat("g", 64) }, func(p *ShareHashInput) { p.ThumbDataHash = strings.Repeat("a", 63) }, func(p *ShareHashInput) { p.ThumbDataHash = "" },
	}
	for i, change := range invalidInputs {
		input := valid
		change(&input)
		result, err := signer.BuildShareWithHash(input)
		if err == nil || result != nil {
			t.Errorf("invalid share input %d accepted", i)
		}
		assertSafeShareError(t, err)
	}
	for _, zero := range []*ShareSigner{nil, {}} {
		result, err := zero.BuildShareWithHash(valid)
		if err == nil || result != nil {
			t.Fatal("zero signer accepted")
		}
		assertSafeShareError(t, err)
	}
}
func assertSafeShareError(t *testing.T, err error) {
	t.Helper()
	var failure *SecurityError
	if !errors.As(err, &failure) {
		t.Fatalf("expected SecurityError, got %T", err)
	}
	for _, secret := range []string{"private-secret-key", "private-title", "private-token", "private-user", "private-password", "images.example", "PRIVATE KEY"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatal("error leaked supplied value")
		}
	}
}

func TestShareSignerSnapshotsAndConcurrentAppSeparation(t *testing.T) {
	first, firstPub, config := newShareTestSigner(t, "wx-first")
	second, secondPub, _ := newShareTestSigner(t, "wx-second")
	// Destruction/reassignment of caller material must not alter an existing signer.
	for i := range config.PrivateKeyPEM {
		config.PrivateKeyPEM[i] = 0
	}
	config.AppID = "wx-mutated"
	type pair struct {
		signer *ShareSigner
		pub    *rsa.PublicKey
		appID  string
		other  *rsa.PublicKey
	}
	var wg sync.WaitGroup
	for _, p := range []pair{{first, firstPub, "wx-first", secondPub}, {second, secondPub, "wx-second", firstPub}} {
		wg.Add(1)
		go func(p pair) {
			defer wg.Done()
			for i := 0; i < 8; i++ {
				result, err := p.signer.BuildShare(ShareInput{Title: "并发分享", Path: "/pages/share?b=2&a=1", ImageURL: "https://images.example/stable", ImageBytes: []byte("stable-image")})
				if err != nil {
					t.Error(err)
					return
				}
				if verifyShareTestSignature(p.pub, p.appID, result) != nil {
					t.Error("signer changed during concurrent use")
				}
				if verifyShareTestSignature(p.other, p.appID, result) == nil {
					t.Error("another key accepted signature")
				}
			}
		}(p)
	}
	wg.Wait()
}
