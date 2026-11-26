// Package settings validates configuration fields and encodes patches without owning persistence or policy.
package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
)

const (
	String        = "string"
	Number        = "number" // Signed 64-bit integer; no floating-point settings.
	Boolean       = "boolean"
	Secret        = "secret"
	Unset         = "unset"
	Defaulted     = "defaulted"
	Configured    = "configured"
	Invalid       = "invalid"
	DecryptFailed = "decrypt_failed"
)

var (
	ErrValidation = errors.New("settings: validation failed")
	ErrCodec      = errors.New("settings: secret codec failed")
)

// Field defines project-owned constraints. Default is an unencrypted stored representation; secrets cannot have defaults.
// Normalize is for string/secret fields. Validate receives a normalized string/int64/bool.
// Callbacks and injected Codec must be concurrency-safe and must not persist changes.
type Field struct {
	Key       string
	Kind      string
	Default   *string
	Min       *int64
	Max       *int64
	MaxBytes  int
	Required  bool
	Clearable bool
	Normalize func(string) (string, error) `json:"-"`
	Validate  func(any) error              `json:"-"`
}

// Codec transforms secret values. Implementations decide ciphertext prefixes and key sources.
type Codec interface {
	Encrypt(context.Context, string) (string, error)
	Decrypt(context.Context, string) (string, error)
}

// Input separates explicit removal from a value. Missing map keys remain untouched.
type Input struct {
	Value any
	Clear bool
}

// Change is an instruction for project persistence; Clear has no implicit SQL meaning.
type Change struct {
	Key     string
	Encoded string
	Clear   bool
}

// Entry exposes independent state, validity and value. View always removes secret values.
type Entry struct {
	Value      any
	State      string
	Configured bool
	Error      error
}

// FieldError deliberately omits input, stored data and callback error messages.
type FieldError struct {
	Key  string
	Code string
}

func (e *FieldError) Error() string { return fmt.Sprintf("settings: %s: %s", e.Key, e.Code) }
func (e *FieldError) Unwrap() error {
	if e.Code == "encrypt_failed" || e.Code == DecryptFailed {
		return ErrCodec
	}
	return ErrValidation
}
func fail(key, code string) error { return &FieldError{key, code} }

// Engine is immutable after construction; it has no database, cache or process-global state.
type Engine struct {
	fields map[string]Field
	keys   []string
	codec  Codec
}

func copyField(f Field) Field {
	if f.Default != nil {
		v := *f.Default
		f.Default = &v
	}
	if f.Min != nil {
		v := *f.Min
		f.Min = &v
	}
	if f.Max != nil {
		v := *f.Max
		f.Max = &v
	}
	return f
}

func New(fields []Field, codec Codec) (*Engine, error) {
	e := &Engine{fields: map[string]Field{}, codec: codec}
	for _, input := range fields {
		f := copyField(input)
		if f.Key == "" {
			return nil, fail("", "empty_key")
		}
		if _, ok := e.fields[f.Key]; ok {
			return nil, fail(f.Key, "duplicate_key")
		}
		if f.Kind != String && f.Kind != Number && f.Kind != Boolean && f.Kind != Secret {
			return nil, fail(f.Key, "invalid_kind")
		}
		if f.MaxBytes < 0 || f.Required && f.Clearable || f.Min != nil && f.Max != nil && *f.Min > *f.Max {
			return nil, fail(f.Key, "invalid_constraints")
		}
		if f.Kind != Number && (f.Min != nil || f.Max != nil) {
			return nil, fail(f.Key, "numeric_bounds_on_non_number")
		}
		if f.Normalize != nil && f.Kind != String && f.Kind != Secret {
			return nil, fail(f.Key, "normalizer_on_non_string")
		}
		if f.Kind == Secret && f.Default != nil {
			return nil, fail(f.Key, "secret_default_forbidden")
		}
		if f.Default != nil {
			if _, err := parse(f, *f.Default); err != nil {
				return nil, fail(f.Key, "invalid_default")
			}
		}
		e.fields[f.Key] = f
		e.keys = append(e.keys, f.Key)
	}
	sort.Strings(e.keys)
	return e, nil
}

// Fields returns defensive copies ordered by key; callback functions themselves are project-owned.
func (e *Engine) Fields() []Field {
	out := make([]Field, 0, len(e.keys))
	for _, k := range e.keys {
		out = append(out, copyField(e.fields[k]))
	}
	return out
}

func integer(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int64:
		return n, true
	case int32:
		return int64(n), true
	case int16:
		return int64(n), true
	case int8:
		return int64(n), true
	case uint:
		if uint64(n) <= math.MaxInt64 {
			return int64(n), true
		}
	case uint64:
		if n <= math.MaxInt64 {
			return int64(n), true
		}
	case uint32:
		return int64(n), true
	case json.Number:
		x, err := strconv.ParseInt(string(n), 10, 64)
		return x, err == nil
	case float64:
		if !math.IsNaN(n) && !math.IsInf(n, 0) && n == math.Trunc(n) && math.Abs(n) <= 9007199254740991 {
			return int64(n), true
		}
	}
	return 0, false
}
func normalize(f Field, value any) (any, error) {
	var out any
	switch f.Kind {
	case Number:
		n, ok := integer(value)
		if !ok {
			return nil, fail(f.Key, "invalid_integer")
		}
		if f.Min != nil && n < *f.Min || f.Max != nil && n > *f.Max {
			return nil, fail(f.Key, "out_of_range")
		}
		out = n
	case Boolean:
		v, ok := value.(bool)
		if !ok {
			return nil, fail(f.Key, "invalid_boolean")
		}
		out = v
	default:
		v, ok := value.(string)
		if !ok {
			return nil, fail(f.Key, "invalid_string")
		}
		if f.Normalize != nil {
			var err error
			v, err = f.Normalize(v)
			if err != nil {
				return nil, fail(f.Key, "normalization_failed")
			}
		}
		if f.Required && v == "" || f.MaxBytes > 0 && len(v) > f.MaxBytes {
			return nil, fail(f.Key, "invalid_length")
		}
		out = v
	}
	if f.Validate != nil {
		if err := f.Validate(out); err != nil {
			return nil, fail(f.Key, "custom_validation_failed")
		}
	}
	return out, nil
}
func parse(f Field, raw string) (any, error) {
	var value any = raw
	switch f.Kind {
	case Number:
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, fail(f.Key, "invalid_integer")
		}
		value = n
	case Boolean:
		if raw != "true" && raw != "false" {
			return nil, fail(f.Key, "invalid_boolean")
		}
		value = raw == "true"
	}
	return normalize(f, value)
}
func stringValue(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case int64:
		return strconv.FormatInt(x, 10)
	case bool:
		return strconv.FormatBool(x)
	}
	return ""
}

// EncodePatch validates every input before calling a Codec. Any failure returns nil changes.
// An empty secret value requires Clear=true, preventing accidental erasure from masked forms.
func (e *Engine) EncodePatch(ctx context.Context, input map[string]Input) ([]Change, error) {
	keys := make([]string, 0, len(input))
	for k := range input {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	values := map[string]any{}
	for _, k := range keys {
		f, ok := e.fields[k]
		if !ok {
			return nil, fail(k, "unknown_field")
		}
		v := input[k]
		if v.Clear {
			if v.Value != nil || !f.Clearable || f.Required {
				return nil, fail(k, "clear_not_allowed")
			}
			continue
		}
		n, err := normalize(f, v.Value)
		if err != nil {
			return nil, err
		}
		if f.Kind == Secret && n == "" {
			return nil, fail(k, "explicit_clear_required")
		}
		values[k] = n
	}
	out := make([]Change, 0, len(keys))
	for _, k := range keys {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if input[k].Clear {
			out = append(out, Change{Key: k, Clear: true})
			continue
		}
		value := stringValue(values[k])
		if e.fields[k].Kind == Secret {
			if e.codec == nil {
				return nil, fail(k, "encrypt_failed")
			}
			var err error
			value, err = e.codec.Encrypt(ctx, value)
			if err != nil || value == "" {
				return nil, fail(k, "encrypt_failed")
			}
		}
		out = append(out, Change{Key: k, Encoded: value})
	}
	return out, nil
}

// Resolve returns runtime values and per-field errors. Missing values use declared defaults.
// Unknown stored keys are ignored. Empty stored secrets are unset; invalid values never fall back silently.
func (e *Engine) Resolve(ctx context.Context, stored map[string]string) map[string]Entry {
	out := make(map[string]Entry, len(e.keys))
	for _, k := range e.keys {
		f := e.fields[k]
		raw, exists := stored[k]
		state := Configured
		if !exists || f.Kind == Secret && raw == "" {
			if f.Default != nil {
				raw = *f.Default
				state = Defaulted
			} else {
				entry := Entry{State: Unset}
				if f.Required {
					entry.Error = fail(k, "required")
				}
				out[k] = entry
				continue
			}
		}
		if f.Kind == Secret {
			if e.codec == nil {
				out[k] = Entry{State: DecryptFailed, Error: fail(k, DecryptFailed)}
				continue
			}
			plain, err := e.codec.Decrypt(ctx, raw)
			if err != nil {
				out[k] = Entry{State: DecryptFailed, Error: fail(k, DecryptFailed)}
				continue
			}
			if plain == "" {
				out[k] = Entry{State: Invalid, Error: fail(k, "empty_secret")}
				continue
			}
			raw = plain
		}
		value, err := parse(f, raw)
		if err != nil {
			out[k] = Entry{State: Invalid, Error: err}
			continue
		}
		out[k] = Entry{Value: value, State: state, Configured: state == Configured}
	}
	return out
}

// View is for administrative display and never returns plaintext/ciphertext for secret fields.
func (e *Engine) View(ctx context.Context, stored map[string]string) map[string]Entry {
	out := e.Resolve(ctx, stored)
	for _, k := range e.keys {
		if e.fields[k].Kind == Secret {
			v := out[k]
			v.Value = nil
			out[k] = v
		}
	}
	return out
}
