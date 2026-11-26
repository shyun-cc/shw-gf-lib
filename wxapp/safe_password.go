package wxapp

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"math/big"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/emmansun/gmsm/sm2"
)

// SafePasswordWireProfile selects a device-verified V02 payload representation.
// The public WeChat guide does not specify the outer encoding, SM2 ciphertext
// format, or total header size. There are no automatic guesses or fallbacks.
// c1c3c2 requires an uncompressed 0x04 SM2 point. For HeaderLength > 2, the exact
// remaining bytes must be supplied as HeaderSuffix, not silently skipped.
type SafePasswordWireProfile struct {
	Encoding         string `json:"encoding"`         // hex or base64 (standard padded, no whitespace)
	CiphertextFormat string `json:"ciphertextFormat"` // asn1 or c1c3c2
	HeaderLength     int    `json:"headerLength"`     // 2..64
	HeaderSuffix     []byte `json:"headerSuffix,omitempty"`
}

// SafePasswordHashProfile is a stable per-credential hash policy chosen by the
// server. HashLength counts lower-case hexadecimal characters (even, 2..1024).
// Kind is pbkdf_hmac_hex or custom. PBKDF requires a stable nonempty Salt and no
// CustomHash. Custom requires an expression and an empty Salt: the input
// component ignores salt when custom-hash is set, so the expression itself must
// include the project's stable salt. This library does not evaluate expressions.
// Store the profile/version with the credential; never randomize it per attempt.
type SafePasswordHashProfile struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	HashLength int    `json:"hashLength"`
	Salt       string `json:"salt,omitempty"`
	CustomHash string `json:"customHash,omitempty"`
}

// SafePasswordBinding contains trusted server identity and operation context.
// Never populate it by trusting identity/purpose values sent by the frontend.
type SafePasswordBinding struct {
	SessionID    string `json:"sessionId"`
	Purpose      string `json:"purpose"`
	CredentialID string `json:"credentialId"`
}

// SafePasswordConfig configures local SM2 validation and the shared replay store.
// ChallengeTTL is required, at least one second, and no greater than MaxClockSkew.
// MaxClockSkew zero defaults to five minutes. Device compatibility for WireProfile
// must be verified by the consuming project; synthetic fixtures are not proof.
type SafePasswordConfig struct {
	AppID         string
	PrivateKeyPEM []byte
	Store         SafePasswordStore
	ChallengeTTL  time.Duration
	MaxClockSkew  time.Duration
	WireProfile   SafePasswordWireProfile
}

// SafePasswordService is an immutable app/key/profile configuration snapshot.
// Its injected Store must support atomic compare-and-consume across instances.
type SafePasswordService struct {
	appID     string
	key       *sm2.PrivateKey
	store     SafePasswordStore
	ttl, skew time.Duration
	wire      SafePasswordWireProfile
}

// SafePasswordChallenge is the safe frontend projection. TimeStamp/ExpiresAt are
// Unix seconds; nonce is a high-entropy decimal string. Certificate package path
// and password length are frontend/project settings and are not inferred here.
type SafePasswordChallenge struct {
	TimeStamp  int64  `json:"timeStamp"`
	Nonce      string `json:"nonce"`
	Salt       string `json:"salt,omitempty"`
	CustomHash string `json:"customHash,omitempty"`
	ExpiresAt  int64  `json:"expiresAt"`
}

// SafePasswordResult is protected hash material for server-side comparison only.
// It is not a login decision. The project owns credential storage/migration,
// authorization, permission checks, rate limits and constant-time comparison.
// Do not return this result to the frontend or log it.
type SafePasswordResult struct {
	Hash      string
	ProfileID string
}

// NewSafePasswordService parses an independent SM2 key snapshot. No in-memory
// fallback is used when the shared replay store is absent or unavailable.
func NewSafePasswordService(c SafePasswordConfig) (*SafePasswordService, error) {
	if !safePasswordText(c.AppID, 256) || c.Store == nil || c.ChallengeTTL < time.Second || c.MaxClockSkew < 0 || !validSafePasswordWire(c.WireProfile) {
		return nil, securityFailure("safe_password_config")
	}
	skew := c.MaxClockSkew
	if skew == 0 {
		skew = 5 * time.Minute
	}
	if c.ChallengeTTL > skew {
		return nil, securityFailure("safe_password_config")
	}
	key, err := parseSafePasswordPrivateKey(c.PrivateKeyPEM)
	if err != nil {
		return nil, securityFailure("safe_password_config")
	}
	wire := c.WireProfile
	wire.HeaderSuffix = bytes.Clone(wire.HeaderSuffix)
	return &SafePasswordService{appID: c.AppID, key: key, store: c.Store, ttl: c.ChallengeTTL, skew: skew, wire: wire}, nil
}
func safePasswordText(value string, max int) bool {
	return value != "" && len(value) <= max && utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}
func validSafePasswordWire(w SafePasswordWireProfile) bool {
	return (w.Encoding == "hex" || w.Encoding == "base64") && (w.CiphertextFormat == "asn1" || w.CiphertextFormat == "c1c3c2") && w.HeaderLength >= 2 && w.HeaderLength <= 64 && len(w.HeaderSuffix) == w.HeaderLength-2
}
func validSafePasswordBinding(b SafePasswordBinding) bool {
	return safePasswordText(b.SessionID, 1024) && safePasswordText(b.Purpose, 256) && safePasswordText(b.CredentialID, 1024)
}
func validSafePasswordProfile(p SafePasswordHashProfile) bool {
	if !safePasswordText(p.ID, 256) || p.HashLength < 2 || p.HashLength > 1024 || p.HashLength%2 != 0 {
		return false
	}
	switch p.Kind {
	case "pbkdf_hmac_hex":
		return safePasswordText(p.Salt, 4096) && p.CustomHash == ""
	case "custom":
		return p.Salt == "" && safePasswordText(p.CustomHash, 4096)
	default:
		return false
	}
}
func validSafePasswordNonce(n string) bool {
	if len(n) < 32 || len(n) > 78 || n[0] == '0' {
		return false
	}
	for _, c := range n {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// IssueChallenge binds a fresh nonce to the server's app, session, purpose,
// credential and stable hash policy. Store persistence must succeed first.
func (s *SafePasswordService) IssueChallenge(ctx context.Context, binding SafePasswordBinding, profile SafePasswordHashProfile) (*SafePasswordChallenge, error) {
	if s == nil || s.key == nil || s.store == nil {
		return nil, securityFailure("safe_password_config")
	}
	if !validSafePasswordBinding(binding) || !validSafePasswordProfile(profile) {
		return nil, securityFailure("safe_password_challenge")
	}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, securityFailure("safe_password_challenge")
	}
	random[0] |= 0x80
	nonce := new(big.Int).SetBytes(random[:]).String()
	now := time.Now()
	issued := now.Unix()
	expires := now.Add(s.ttl).Unix()
	record := SafePasswordRecord{Version: 1, AppID: s.appID, Binding: binding, Profile: profile, WireProfile: s.wire, Nonce: nonce, TimeStamp: issued, ExpiresAt: expires}
	// Store implementations are trusted dependencies, but do not let them retain a
	// mutable alias to this service's wire profile byte slice.
	record.WireProfile.HeaderSuffix = bytes.Clone(s.wire.HeaderSuffix)
	if err := s.store.Save(ctx, record, time.Until(time.Unix(expires, 0))); err != nil {
		return nil, securityFailure("safe_password_store")
	}
	return &SafePasswordChallenge{TimeStamp: issued, Nonce: nonce, Salt: profile.Salt, CustomHash: profile.CustomHash, ExpiresAt: expires}, nil
}

// Validate decrypts V02_ payloads, checks the exact issued record, then atomically
// consumes it. Only the consume winner receives hash material; invalid bindings,
// ciphertexts, timestamps or profiles never delete the challenge. No cleartext
// password is returned. Parsing is strictly the configured wire representation.
func (s *SafePasswordService) Validate(ctx context.Context, binding SafePasswordBinding, nonce, ciphertext string) (*SafePasswordResult, error) {
	if s == nil || s.key == nil || s.store == nil {
		return nil, securityFailure("safe_password_config")
	}
	if !validSafePasswordBinding(binding) || !validSafePasswordNonce(nonce) {
		return nil, securityFailure("safe_password_validate")
	}
	if len(ciphertext) > 32<<10 || !strings.HasPrefix(ciphertext, "V02_") {
		return nil, securityFailure("safe_password_decrypt")
	}
	record, err := s.store.Load(ctx, s.appID, nonce)
	if err != nil {
		return nil, securityFailure("safe_password_store")
	}
	now := time.Now().Unix()
	if record == nil || record.Version != 1 || record.AppID != s.appID || record.Nonce != nonce || record.Binding != binding || !validSafePasswordProfile(record.Profile) || !equalSafePasswordWire(record.WireProfile, s.wire) || record.TimeStamp <= 0 || record.TimeStamp > now || record.ExpiresAt <= now || record.ExpiresAt <= record.TimeStamp {
		return nil, securityFailure("safe_password_validate")
	}
	// Bound integer epochs before subtracting; neither malicious far-future nor
	// negative values can overflow the timestamp age calculation.
	if now-record.TimeStamp > int64(s.skew/time.Second) || record.ExpiresAt-record.TimeStamp > int64(s.skew/time.Second)+1 {
		return nil, securityFailure("safe_password_validate")
	}
	encrypted, err := s.decodeSafePasswordCiphertext(ciphertext[4:])
	if err != nil {
		return nil, err
	}
	plain, err := sm2.Decrypt(s.key, encrypted)
	if err != nil {
		return nil, securityFailure("safe_password_decrypt")
	}
	defer clear(plain)
	if len(plain) <= s.wire.HeaderLength || plain[0] != 0 {
		return nil, securityFailure("safe_password_validate")
	}
	expectedKind := byte(7)
	if record.Profile.Kind == "custom" {
		expectedKind = 0
	}
	if plain[1] != expectedKind || !bytes.Equal(plain[2:s.wire.HeaderLength], s.wire.HeaderSuffix) {
		return nil, securityFailure("safe_password_validate")
	}
	fields := bytes.SplitN(plain[s.wire.HeaderLength:], []byte{0}, 4)
	if len(fields) != 4 || len(fields[3]) == 0 || string(fields[0]) != strconv.FormatInt(record.TimeStamp, 10) || string(fields[2]) != nonce || !validSafePasswordHash(fields[1], record.Profile.HashLength) {
		return nil, securityFailure("safe_password_validate")
	}
	// Verify expiry once more after decryption; callers never receive a hash from
	// an expired challenge even if expensive validation crossed the boundary.
	if time.Now().Unix() >= record.ExpiresAt {
		return nil, securityFailure("safe_password_validate")
	}
	consumed, err := s.store.Consume(ctx, *record)
	if err != nil {
		return nil, securityFailure("safe_password_store")
	}
	if !consumed {
		return nil, securityFailure("safe_password_replay")
	}
	if time.Now().Unix() >= record.ExpiresAt {
		return nil, securityFailure("safe_password_validate")
	}
	return &SafePasswordResult{Hash: string(fields[1]), ProfileID: record.Profile.ID}, nil
}
func equalSafePasswordWire(a, b SafePasswordWireProfile) bool {
	return a.Encoding == b.Encoding && a.CiphertextFormat == b.CiphertextFormat && a.HeaderLength == b.HeaderLength && bytes.Equal(a.HeaderSuffix, b.HeaderSuffix)
}
func validSafePasswordHash(hash []byte, length int) bool {
	if len(hash) != length {
		return false
	}
	for _, c := range hash {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func (s *SafePasswordService) decodeSafePasswordCiphertext(input string) ([]byte, error) {
	var raw []byte
	var err error
	switch s.wire.Encoding {
	case "hex":
		raw, err = hex.DecodeString(input)
	case "base64":
		raw, err = base64.StdEncoding.Strict().DecodeString(input)
		if err == nil && base64.StdEncoding.EncodeToString(raw) != input {
			err = securityFailure("safe_password_decrypt")
		}
	default:
		return nil, securityFailure("safe_password_config")
	}
	if err != nil || len(raw) == 0 {
		return nil, securityFailure("safe_password_decrypt")
	}
	if s.wire.CiphertextFormat == "asn1" {
		if raw[0] != 0x30 {
			return nil, securityFailure("safe_password_decrypt")
		}
	} else if s.wire.CiphertextFormat == "c1c3c2" {
		if raw[0] != 0x04 || len(raw) <= 97 {
			return nil, securityFailure("safe_password_decrypt")
		}
	} else {
		return nil, securityFailure("safe_password_config")
	}
	return raw, nil
}
