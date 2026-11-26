package wxapp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"
	"time"

	"github.com/gogf/gf/v2/frame/g"
)

// SafePasswordRecord is the server-only replay record. Never serialize it into
// a frontend response: it contains private identity and credential bindings.
// Store implementations must persist complete records and compare every field
// atomically at consumption, not merely check that the nonce exists.
type SafePasswordRecord struct {
	Version     int                     `json:"version"`
	AppID       string                  `json:"appid"`
	Binding     SafePasswordBinding     `json:"binding"`
	Profile     SafePasswordHashProfile `json:"profile"`
	WireProfile SafePasswordWireProfile `json:"wireProfile"`
	Nonce       string                  `json:"nonce"`
	TimeStamp   int64                   `json:"timeStamp"`
	ExpiresAt   int64                   `json:"expiresAt"`
}

// SafePasswordStore is a shared cross-process challenge store. Save must reject
// collisions (never overwrite), Load must not consume, and Consume must compare
// the exact expected complete record and delete it in one atomic operation.
// Missing Load records are (nil,nil); missing/mismatching Consume is (false,nil).
// Any backend error must be returned; callers fail closed with no local fallback.
type SafePasswordStore interface {
	Save(context.Context, SafePasswordRecord, time.Duration) error
	Load(context.Context, string, string) (*SafePasswordRecord, error)
	Consume(context.Context, SafePasswordRecord) (bool, error)
}

// RedisSafePasswordStore uses the project's registered GoFrame Redis/Valkey
// adapter and default connection. It stores no local challenges and logs no
// records or backend errors. A namespace is required to isolate applications
// and deployments sharing a cache; app/nonce keys are hashed within it.
type RedisSafePasswordStore struct{ namespace string }

// NewRedisSafePasswordStore creates a store without connecting to the backend.
// Namespace accepts 1..128 ASCII letters/digits/colon/underscore/hyphen.
func NewRedisSafePasswordStore(namespace string) (*RedisSafePasswordStore, error) {
	if len(namespace) == 0 || len(namespace) > 128 {
		return nil, securityFailure("safe_password_store")
	}
	for _, c := range namespace {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune(":_-", c)) {
			return nil, securityFailure("safe_password_store")
		}
	}
	return &RedisSafePasswordStore{namespace: namespace}, nil
}
func (s *RedisSafePasswordStore) key(appID, nonce string) (string, error) {
	if s == nil || s.namespace == "" || !safePasswordText(appID, 256) || !validSafePasswordNonce(nonce) {
		return "", securityFailure("safe_password_store")
	}
	identity, _ := json.Marshal([2]string{appID, nonce})
	digest := sha256.Sum256(identity)
	return s.namespace + ":safe-password:" + hex.EncodeToString(digest[:]), nil
}
func encodeSafePasswordRecord(record SafePasswordRecord) ([]byte, error) {
	raw, err := json.Marshal(record)
	if err != nil || len(raw) > 16<<10 {
		return nil, securityFailure("safe_password_store")
	}
	return raw, nil
}

// Save persists a record with NX and a required expiration. It never replaces
// an existing nonce and returns a fixed safe error on backend failure/collision.
func (s *RedisSafePasswordStore) Save(ctx context.Context, record SafePasswordRecord, ttl time.Duration) error {
	key, err := s.key(record.AppID, record.Nonce)
	if err != nil {
		return err
	}
	if ttl < time.Millisecond {
		return securityFailure("safe_password_store")
	}
	raw, err := encodeSafePasswordRecord(record)
	if err != nil {
		return err
	}
	client := g.Redis()
	if client == nil {
		return securityFailure("safe_password_store")
	}
	result, err := client.Do(ctx, "SET", key, string(raw), "NX", "PX", ttl.Milliseconds())
	if err != nil || result == nil || result.String() != "OK" {
		return securityFailure("safe_password_store")
	}
	return nil
}

// Load returns the complete record without deleting it. Malformed, oversized or
// identity-mismatched cache data is rejected rather than treated as a new nonce.
func (s *RedisSafePasswordStore) Load(ctx context.Context, appID, nonce string) (*SafePasswordRecord, error) {
	key, err := s.key(appID, nonce)
	if err != nil {
		return nil, err
	}
	client := g.Redis()
	if client == nil {
		return nil, securityFailure("safe_password_store")
	}
	const readScript = `local value=redis.call("GET",KEYS[1]); if not value then return false end; if string.len(value)>16384 then return redis.error_reply("invalid record size") end; return value`
	value, err := client.Eval(ctx, readScript, 1, []string{key}, nil)
	if err != nil {
		return nil, securityFailure("safe_password_store")
	}
	if value == nil || value.IsNil() {
		return nil, nil
	}
	raw := value.Bytes()
	if len(raw) == 0 || len(raw) > 16<<10 {
		return nil, securityFailure("safe_password_store")
	}
	var record SafePasswordRecord
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&record) != nil {
		return nil, securityFailure("safe_password_store")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, securityFailure("safe_password_store")
	}
	if record.AppID != appID || record.Nonce != nonce {
		return nil, securityFailure("safe_password_store")
	}
	return &record, nil
}

// Consume atomically compares the exact serialized record and deletes only a
// match. A changed binding/profile, a concurrent winner or an expired key fails.
func (s *RedisSafePasswordStore) Consume(ctx context.Context, record SafePasswordRecord) (bool, error) {
	key, err := s.key(record.AppID, record.Nonce)
	if err != nil {
		return false, err
	}
	raw, err := encodeSafePasswordRecord(record)
	if err != nil {
		return false, err
	}
	client := g.Redis()
	if client == nil {
		return false, securityFailure("safe_password_store")
	}
	const script = `if redis.call("GET",KEYS[1]) == ARGV[1] then return redis.call("DEL",KEYS[1]) else return 0 end`
	result, err := client.Eval(ctx, script, 1, []string{key}, []any{string(raw)})
	if err != nil || result == nil {
		return false, securityFailure("safe_password_store")
	}
	return result.Int() == 1, nil
}
