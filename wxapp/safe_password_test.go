package wxapp

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emmansun/gmsm/sm2"
	"github.com/emmansun/gmsm/smx509"
	"github.com/shyun-cc/shw-gf-lib/internal/testredis"
)

type safePasswordFixture struct {
	config  SafePasswordConfig
	service *SafePasswordService
	key     *sm2.PrivateKey
	store   *RedisSafePasswordStore
	binding SafePasswordBinding
	profile SafePasswordHashProfile
}

func newSafePasswordFixture(t *testing.T) safePasswordFixture {
	t.Helper()
	testredis.Start(t)
	key, err := sm2.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := smx509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewRedisSafePasswordStore("unit:keyboard")
	if err != nil {
		t.Fatal(err)
	}
	config := SafePasswordConfig{AppID: "wx-keyboard", PrivateKeyPEM: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), Store: store, ChallengeTTL: time.Minute, WireProfile: SafePasswordWireProfile{Encoding: "base64", CiphertextFormat: "asn1", HeaderLength: 2}}
	service, err := NewSafePasswordService(config)
	if err != nil {
		t.Fatal(err)
	}
	return safePasswordFixture{config: config, service: service, key: key, store: store, binding: SafePasswordBinding{SessionID: "private-session", Purpose: "login", CredentialID: "private-credential"}, profile: SafePasswordHashProfile{ID: "pbkdf-v1", Kind: "pbkdf_hmac_hex", HashLength: 64, Salt: "stable-credential-salt"}}
}
func issueSafePassword(t *testing.T, f safePasswordFixture) *SafePasswordChallenge {
	t.Helper()
	c, err := f.service.IssueChallenge(context.Background(), f.binding, f.profile)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// Construct plaintext/encrypt independently from production parsing. These are
// synthetic protocol fixtures, not captured WeChat device output.
func encryptSafePasswordFixture(t *testing.T, key *sm2.PrivateKey, wire SafePasswordWireProfile, plain []byte) string {
	t.Helper()
	var encrypted []byte
	var err error
	if wire.CiphertextFormat == "asn1" {
		encrypted, err = sm2.EncryptASN1(rand.Reader, &key.PublicKey, plain)
	} else {
		encrypted, err = sm2.Encrypt(rand.Reader, &key.PublicKey, plain, sm2.NewPlainEncrypterOpts(sm2.MarshalUncompressed, sm2.C1C3C2))
	}
	if err != nil {
		t.Fatal(err)
	}
	if wire.Encoding == "hex" {
		return "V02_" + hex.EncodeToString(encrypted)
	}
	return "V02_" + base64.StdEncoding.EncodeToString(encrypted)
}
func safePasswordFixturePlain(c *SafePasswordChallenge, wire SafePasswordWireProfile, kind, hash string) []byte {
	header := make([]byte, wire.HeaderLength)
	if kind == "pbkdf_hmac_hex" {
		header[1] = 7
	}
	copy(header[2:], wire.HeaderSuffix)
	plain := append(header, []byte(strconv.FormatInt(c.TimeStamp, 10))...)
	plain = append(plain, 0)
	plain = append(plain, []byte(hash)...)
	plain = append(plain, 0)
	plain = append(plain, []byte(c.Nonce)...)
	plain = append(plain, 0)
	// Random tail is binary and may contain embedded NUL, so split exactly 3 times.
	return append(plain, []byte{0x80, 0, 0x01, 0xff}...)
}
func assertSafePasswordError(t *testing.T, err error) {
	t.Helper()
	var e *SecurityError
	if !errors.As(err, &e) {
		t.Fatalf("expected safe failure, got %T", err)
	}
	for _, secret := range []string{"private-session", "private-credential", "stable-credential-salt", "PRIVATE KEY", "backend-secret", "ciphertext-secret", strings.Repeat("a", 64)} {
		if strings.Contains(err.Error(), secret) {
			t.Fatal("sensitive material escaped error")
		}
	}
}

func TestSafePasswordPublicValidationAndProjection(t *testing.T) {
	f := newSafePasswordFixture(t)
	challenge := issueSafePassword(t, f)
	if !validSafePasswordNonce(challenge.Nonce) || challenge.TimeStamp <= 0 || challenge.ExpiresAt <= challenge.TimeStamp || challenge.Salt != f.profile.Salt {
		t.Fatal("invalid frontend challenge")
	}
	raw, err := json.Marshal(challenge)
	if err != nil {
		t.Fatal(err)
	}
	var projection map[string]any
	_ = json.Unmarshal(raw, &projection)
	if len(projection) != 4 || projection["timeStamp"] == nil || projection["nonce"] == nil || projection["salt"] == nil || projection["expiresAt"] == nil {
		t.Fatal("frontend challenge contract changed")
	}
	for _, hidden := range []string{"private-session", "private-credential", "wx-keyboard", "pbkdf-v1"} {
		if strings.Contains(string(raw), hidden) {
			t.Fatal("server binding leaked into frontend projection")
		}
	}
	hash := strings.Repeat("a", 64)
	wire := encryptSafePasswordFixture(t, f.key, f.config.WireProfile, safePasswordFixturePlain(challenge, f.config.WireProfile, f.profile.Kind, hash))
	result, err := f.service.Validate(context.Background(), f.binding, challenge.Nonce, wire)
	if err != nil || result == nil || result.Hash != hash || result.ProfileID != f.profile.ID {
		t.Fatal("valid protected hash rejected", err)
	}
	if _, err = f.service.Validate(context.Background(), f.binding, challenge.Nonce, wire); err == nil {
		t.Fatal("challenge replay accepted")
	}
}

func TestSafePasswordAllExplicitWireProfiles(t *testing.T) {
	f := newSafePasswordFixture(t)
	for _, encoding := range []string{"hex", "base64"} {
		for _, format := range []string{"asn1", "c1c3c2"} {
			for _, headerLength := range []int{2, 5} {
				t.Run(encoding+"-"+format+"-"+strconv.Itoa(headerLength), func(t *testing.T) {
					config := f.config
					config.WireProfile = SafePasswordWireProfile{Encoding: encoding, CiphertextFormat: format, HeaderLength: headerLength}
					if headerLength > 2 {
						config.WireProfile.HeaderSuffix = []byte{0x01, 0x02, 0x03}
					}
					service, err := NewSafePasswordService(config)
					if err != nil {
						t.Fatal(err)
					}
					challenge, err := service.IssueChallenge(context.Background(), f.binding, f.profile)
					if err != nil {
						t.Fatal(err)
					}
					wire := encryptSafePasswordFixture(t, f.key, config.WireProfile, safePasswordFixturePlain(challenge, config.WireProfile, f.profile.Kind, strings.Repeat("b", 64)))
					if _, err = service.Validate(context.Background(), f.binding, challenge.Nonce, wire); err != nil {
						t.Fatal(err)
					}
				})
			}
		}
	}
}

func TestSafePasswordRejectsInvalidPayloadWithoutConsuming(t *testing.T) {
	f := newSafePasswordFixture(t)
	cases := []string{"prefix", "outer-format", "outer-whitespace", "ciphertext-tamper", "empty", "large", "wrong-key", "wrong-kind", "unknown-kind", "wrong-time", "noncanonical-time", "wrong-nonce", "upper-hash", "short-hash", "bad-hash", "missing-random", "missing-field", "wrong-binding-session", "wrong-binding-purpose", "wrong-binding-credential", "wrong-wire", "wrong-header-suffix"}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			service := f.service
			config := f.config
			if name == "wrong-header-suffix" {
				config.WireProfile.HeaderLength = 3
				config.WireProfile.HeaderSuffix = []byte{0x01}
				var err error
				service, err = NewSafePasswordService(config)
				if err != nil {
					t.Fatal(err)
				}
			}
			challenge, err := service.IssueChallenge(context.Background(), f.binding, f.profile)
			if err != nil {
				t.Fatal(err)
			}
			plain := safePasswordFixturePlain(challenge, config.WireProfile, f.profile.Kind, strings.Repeat("a", 64))
			validPlain := bytes.Clone(plain)
			binding := f.binding
			key := f.key
			wireProfile := config.WireProfile
			switch name {
			case "wrong-key":
				key, err = sm2.GenerateKey(rand.Reader)
				if err != nil {
					t.Fatal(err)
				}
			case "wrong-kind":
				plain[1] = 0
			case "unknown-kind":
				plain[1] = 8
			case "wrong-time":
				plain = bytes.Replace(plain, []byte(strconv.FormatInt(challenge.TimeStamp, 10)), []byte(strconv.FormatInt(challenge.TimeStamp+1, 10)), 1)
			case "noncanonical-time":
				plain = append(plain[:wireProfile.HeaderLength], append([]byte("0"), plain[wireProfile.HeaderLength:]...)...)
			case "wrong-nonce":
				plain = bytes.Replace(plain, []byte(challenge.Nonce), []byte(strings.Repeat("9", len(challenge.Nonce))), 1)
			case "upper-hash":
				plain = bytes.Replace(plain, []byte(strings.Repeat("a", 64)), []byte(strings.Repeat("A", 64)), 1)
			case "short-hash":
				plain = bytes.Replace(plain, []byte(strings.Repeat("a", 64)), []byte("abcd"), 1)
			case "bad-hash":
				plain = bytes.Replace(plain, []byte(strings.Repeat("a", 64)), []byte(strings.Repeat("g", 64)), 1)
			case "missing-random":
				plain = plain[:len(plain)-4]
			case "missing-field":
				plain = []byte{0, 7, '1'}
			case "wrong-binding-session":
				binding.SessionID = "other-session"
			case "wrong-binding-purpose":
				binding.Purpose = "password-reset"
			case "wrong-binding-credential":
				binding.CredentialID = "other-credential"
			case "wrong-wire":
				wireProfile.CiphertextFormat = "c1c3c2"
			case "wrong-header-suffix":
				plain[2] = 0x02
			}
			ciphertext := encryptSafePasswordFixture(t, key, wireProfile, plain)
			switch name {
			case "prefix":
				ciphertext = "V01_" + ciphertext[4:]
			case "outer-format":
				ciphertext = "V02_" + hex.EncodeToString([]byte(ciphertext[4:]))
			case "outer-whitespace":
				ciphertext += "\n"
			case "ciphertext-tamper":
				ciphertext = ciphertext[:len(ciphertext)-8] + "AAAAAA=="
			case "empty":
				ciphertext = "V02_"
			case "large":
				ciphertext = "V02_" + strings.Repeat("A", 33<<10)
			}
			result, err := service.Validate(context.Background(), binding, challenge.Nonce, ciphertext)
			if err == nil || result != nil {
				t.Fatal("invalid protected payload accepted")
			}
			assertSafePasswordError(t, err)
			valid := encryptSafePasswordFixture(t, f.key, config.WireProfile, validPlain)
			if _, err = service.Validate(context.Background(), f.binding, challenge.Nonce, valid); err != nil {
				t.Fatal("invalid attempt consumed valid challenge", err)
			}
		})
	}
}

func TestSafePasswordCustomHashPolicyAndStableCredentialSalt(t *testing.T) {
	f := newSafePasswordFixture(t)
	profile := SafePasswordHashProfile{ID: "custom-sm3-v1", Kind: "custom", HashLength: 64, CustomHash: "sm3(password + 'credential-stable-salt')"}
	first, err := f.service.IssueChallenge(context.Background(), f.binding, profile)
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.service.IssueChallenge(context.Background(), f.binding, profile)
	if err != nil {
		t.Fatal(err)
	}
	if first.Nonce == second.Nonce || first.CustomHash != second.CustomHash || first.Salt != "" || second.Salt != "" {
		t.Fatal("custom hash policy changed per attempt")
	}
	hash := strings.Repeat("c", 64)
	ciphertext := encryptSafePasswordFixture(t, f.key, f.config.WireProfile, safePasswordFixturePlain(first, f.config.WireProfile, profile.Kind, hash))
	result, err := f.service.Validate(context.Background(), f.binding, first.Nonce, ciphertext)
	if err != nil || result.ProfileID != profile.ID || result.Hash != hash {
		t.Fatal("custom hash profile rejected", err)
	}
	a := issueSafePassword(t, f)
	b := issueSafePassword(t, f)
	if a.Salt != b.Salt || a.Salt != f.profile.Salt || a.Nonce == b.Nonce {
		t.Fatal("per-credential salt became a per-attempt nonce")
	}
}

func TestSafePasswordConcurrentReplayAcrossServiceInstances(t *testing.T) {
	f := newSafePasswordFixture(t)
	other, err := NewSafePasswordService(f.config)
	if err != nil {
		t.Fatal(err)
	}
	challenge := issueSafePassword(t, f)
	ciphertext := encryptSafePasswordFixture(t, f.key, f.config.WireProfile, safePasswordFixturePlain(challenge, f.config.WireProfile, f.profile.Kind, strings.Repeat("a", 64)))
	var successes atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			service := f.service
			if i%2 == 1 {
				service = other
			}
			result, err := service.Validate(context.Background(), f.binding, challenge.Nonce, ciphertext)
			if err == nil {
				if result == nil {
					t.Error("nil success result")
				}
				successes.Add(1)
			} else {
				var safe *SecurityError
				if !errors.As(err, &safe) {
					t.Error("unsafe concurrent error")
				}
			}
		}(i)
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatalf("expected one consume winner, got %d", successes.Load())
	}
}

func TestSafePasswordExpiredAndUntrustedRecordTimes(t *testing.T) {
	f := newSafePasswordFixture(t)
	for _, name := range []string{"expired", "future", "negative", "max-int64", "old", "invalid-profile", "invalid-version", "wire-profile"} {
		t.Run(name, func(t *testing.T) {
			challenge := issueSafePassword(t, f)
			record, err := f.store.Load(context.Background(), f.config.AppID, challenge.Nonce)
			if err != nil || record == nil {
				t.Fatal("missing record", err)
			}
			switch name {
			case "expired":
				record.ExpiresAt = time.Now().Unix() - 1
			case "future":
				record.TimeStamp = time.Now().Unix() + 10
				record.ExpiresAt = record.TimeStamp + 30
			case "negative":
				record.TimeStamp = math.MinInt64
			case "max-int64":
				record.TimeStamp = math.MaxInt64
				record.ExpiresAt = math.MaxInt64
			case "old":
				record.TimeStamp = time.Now().Add(-10 * time.Minute).Unix()
			case "invalid-profile":
				record.Profile.HashLength = 0
			case "invalid-version":
				record.Version = 2
			case "wire-profile":
				record.WireProfile.Encoding = "hex"
			}
			overwriteSafePasswordRecord(t, f.store, *record)
			ciphertext := encryptSafePasswordFixture(t, f.key, f.config.WireProfile, safePasswordFixturePlain(challenge, f.config.WireProfile, f.profile.Kind, strings.Repeat("a", 64)))
			result, err := f.service.Validate(context.Background(), f.binding, challenge.Nonce, ciphertext)
			if err == nil || result != nil {
				t.Fatal("invalid record accepted")
			}
			assertSafePasswordError(t, err)
		})
	}
}

// This external anchor is the published gmsm v0.44.1
// sm2/example_test.go ExamplePrivateKey_Decrypt fixture, not a device payload.
func TestSafePasswordPrimarySM2GoldenVector(t *testing.T) {
	keyBytes, _ := hex.DecodeString("6c5a0a0b2eed3cbec3e4f1252bfe0e28c504a1c6bf1999eebb0af9ef0f8e6c85")
	key, err := sm2.NewPrivateKey(keyBytes)
	if err != nil {
		t.Fatal(err)
	}
	const encoded = "308194022100bd31001ce8d39a4a0119ff96d71334cd12d8b75bbc780f5bfc6e1efab535e85a02201839c075ff8bf761dcbe185c9750816410517001d6a130f6ab97fb23337cce150420ea82bd58d6a5394eb468a769ab48b6a26870ca075377eb06663780c920ea5ee0042be22abcf48e56ae9d29ac770d9de0d6b7094a874a2f8d26c26e0b1daaf4ff50a484b88163d04785b04585bb"
	service := &SafePasswordService{wire: SafePasswordWireProfile{Encoding: "hex", CiphertextFormat: "asn1", HeaderLength: 2}}
	raw, err := service.decodeSafePasswordCiphertext(encoded)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := sm2.Decrypt(key, raw)
	if err != nil || string(plain) != "send reinforcements, we're going to advance" {
		t.Fatal("primary SM2 fixture failed", err)
	}
	if _, err = service.decodeSafePasswordCiphertext(encoded + "00"); err != nil {
		t.Fatal("outer hex must decode before ASN1 strict validation")
	}
	if _, err = sm2.Decrypt(key, append(raw, 0)); err == nil {
		t.Fatal("ASN1 trailing bytes accepted")
	}
}

func TestSafePasswordConfigurationAndInputValidation(t *testing.T) {
	f := newSafePasswordFixture(t)
	mutations := []func(*SafePasswordConfig){func(c *SafePasswordConfig) { c.AppID = "" }, func(c *SafePasswordConfig) { c.Store = nil }, func(c *SafePasswordConfig) { c.PrivateKeyPEM = []byte("secret-invalid") }, func(c *SafePasswordConfig) { c.ChallengeTTL = 0 }, func(c *SafePasswordConfig) { c.ChallengeTTL = time.Millisecond }, func(c *SafePasswordConfig) { c.ChallengeTTL = 6 * time.Minute }, func(c *SafePasswordConfig) { c.MaxClockSkew = -1 }, func(c *SafePasswordConfig) { c.WireProfile.Encoding = "auto" }, func(c *SafePasswordConfig) { c.WireProfile.CiphertextFormat = "auto" }, func(c *SafePasswordConfig) { c.WireProfile.HeaderLength = 1 }, func(c *SafePasswordConfig) { c.WireProfile.HeaderLength = 65 }, func(c *SafePasswordConfig) { c.WireProfile.HeaderLength = 3 }}
	for i, mutate := range mutations {
		config := f.config
		mutate(&config)
		service, err := NewSafePasswordService(config)
		if err == nil || service != nil {
			t.Errorf("invalid configuration %d accepted", i)
		}
		assertSafePasswordError(t, err)
	}
	badProfiles := []SafePasswordHashProfile{{}, {ID: "v1", Kind: "unknown", HashLength: 64, Salt: "stable"}, {ID: "v1", Kind: "custom", HashLength: 64, Salt: "ignored-salt", CustomHash: "sm3(password)"}, {ID: "v1", Kind: "pbkdf_hmac_hex", HashLength: 64, Salt: "stable", CustomHash: "sm3(password)"}, {ID: "v1", Kind: "pbkdf_hmac_hex", HashLength: 63, Salt: "stable"}, {ID: "v1", Kind: "pbkdf_hmac_hex", HashLength: 64}}
	for _, profile := range badProfiles {
		challenge, err := f.service.IssueChallenge(context.Background(), f.binding, profile)
		if err == nil || challenge != nil {
			t.Fatal("invalid hash profile accepted")
		}
		assertSafePasswordError(t, err)
	}
	if _, err := f.service.IssueChallenge(context.Background(), SafePasswordBinding{}, f.profile); err == nil {
		t.Fatal("unbound challenge issued")
	}
	for _, nonce := range []string{"", "0", "1e30", strings.Repeat("1", 79)} {
		result, err := f.service.Validate(context.Background(), f.binding, nonce, "V02_ciphertext-secret")
		if err == nil || result != nil {
			t.Fatal("invalid nonce accepted")
		}
		assertSafePasswordError(t, err)
	}
	config := f.config
	config.WireProfile.HeaderLength = 3
	config.WireProfile.HeaderSuffix = []byte{0x01}
	service, err := NewSafePasswordService(config)
	if err != nil {
		t.Fatal(err)
	}
	config.WireProfile.HeaderSuffix[0] = 0xff
	config.PrivateKeyPEM[0] = '!'
	if service.wire.HeaderSuffix[0] != 0x01 {
		t.Fatal("wire profile aliases caller configuration")
	}
}

type failedSafePasswordStore struct {
	base                         SafePasswordStore
	saveErr, loadErr, consumeErr bool
}

func (s failedSafePasswordStore) Save(ctx context.Context, r SafePasswordRecord, ttl time.Duration) error {
	if s.saveErr {
		return errors.New("backend-secret save")
	}
	return s.base.Save(ctx, r, ttl)
}
func (s failedSafePasswordStore) Load(ctx context.Context, a, n string) (*SafePasswordRecord, error) {
	if s.loadErr {
		return nil, errors.New("backend-secret load")
	}
	return s.base.Load(ctx, a, n)
}
func (s failedSafePasswordStore) Consume(ctx context.Context, r SafePasswordRecord) (bool, error) {
	if s.consumeErr {
		return false, errors.New("backend-secret consume")
	}
	return s.base.Consume(ctx, r)
}
func TestSafePasswordStoreFailuresFailClosed(t *testing.T) {
	f := newSafePasswordFixture(t)
	for _, stage := range []string{"save", "load", "consume"} {
		t.Run(stage, func(t *testing.T) {
			config := f.config
			config.Store = failedSafePasswordStore{base: f.store, saveErr: stage == "save", loadErr: stage == "load", consumeErr: stage == "consume"}
			service, err := NewSafePasswordService(config)
			if err != nil {
				t.Fatal(err)
			}
			challenge, err := service.IssueChallenge(context.Background(), f.binding, f.profile)
			if stage == "save" {
				if err == nil || challenge != nil {
					t.Fatal("save failure issued challenge")
				}
				assertSafePasswordError(t, err)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			ciphertext := encryptSafePasswordFixture(t, f.key, f.config.WireProfile, safePasswordFixturePlain(challenge, f.config.WireProfile, f.profile.Kind, strings.Repeat("a", 64)))
			result, err := service.Validate(context.Background(), f.binding, challenge.Nonce, ciphertext)
			if err == nil || result != nil {
				t.Fatal("backend failure returned hash")
			}
			assertSafePasswordError(t, err)
			if _, err = f.service.Validate(context.Background(), f.binding, challenge.Nonce, ciphertext); err != nil {
				t.Fatal("backend failure consumed challenge")
			}
		})
	}
}
