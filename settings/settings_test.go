package settings

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type fakeCodec struct {
	calls int
	fail  bool
}

func (c *fakeCodec) Encrypt(_ context.Context, s string) (string, error) {
	c.calls++
	if c.fail {
		return "", errors.New("leaked " + s)
	}
	return "encrypted:" + s, nil
}
func (c *fakeCodec) Decrypt(_ context.Context, s string) (string, error) {
	if c.fail {
		return "", errors.New("leaked " + s)
	}
	if !strings.HasPrefix(s, "encrypted:") {
		return "", errors.New("bad ciphertext " + s)
	}
	return strings.TrimPrefix(s, "encrypted:"), nil
}
func TestPatchAndProjection(t *testing.T) {
	ctx := context.Background()
	min, max := int64(1), int64(100)
	defaultValue := "10"
	codec := &fakeCodec{}
	e, err := New([]Field{{Key: "count", Kind: Number, Min: &min, Max: &max, Default: &defaultValue}, {Key: "secret", Kind: Secret, Clearable: true}, {Key: "enabled", Kind: Boolean}}, codec)
	if err != nil {
		t.Fatal(err)
	}
	changes, err := e.EncodePatch(ctx, map[string]Input{"count": {Value: json.Number("12")}, "secret": {Value: "private"}, "enabled": {Value: false}})
	if err != nil || len(changes) != 3 || changes[0].Key != "count" || changes[0].Encoded != "12" {
		t.Fatal(changes, err)
	}
	runtime := e.Resolve(ctx, map[string]string{"secret": "encrypted:private"})
	if runtime["secret"].Value != "private" || runtime["count"].Value != int64(10) || runtime["count"].State != Defaulted {
		t.Fatal(runtime)
	}
	view := e.View(ctx, map[string]string{"secret": "encrypted:private"})
	if view["secret"].Value != nil || !view["secret"].Configured {
		t.Fatal(view)
	}
	cleared, err := e.EncodePatch(ctx, map[string]Input{"secret": {Clear: true}})
	if err != nil || len(cleared) != 1 || !cleared[0].Clear {
		t.Fatal(cleared, err)
	}
	changes, err = e.EncodePatch(ctx, nil)
	if err != nil || len(changes) != 0 {
		t.Fatal(changes, err)
	}
	codec.fail = true
	bad := e.View(ctx, map[string]string{"secret": "encrypted:private"})
	if bad["secret"].State != DecryptFailed || strings.Contains(bad["secret"].Error.Error(), "private") {
		t.Fatal(bad)
	}
}
func TestValidationPrecedesEncoding(t *testing.T) {
	codec := &fakeCodec{}
	e, _ := New([]Field{{Key: "a", Kind: Secret}, {Key: "z", Kind: Number}}, codec)
	for _, input := range []map[string]Input{
		{"a": {Value: "private"}, "z": {Value: 1.5}},
		{"unknown": {Value: 1}},
		{"a": {Clear: true}},
		{"z": {Value: float64(9007199254740992)}},
		{"z": {Value: json.Number("9223372036854775808")}},
		{"z": {Value: nil}},
	} {
		out, err := e.EncodePatch(context.Background(), input)
		if err == nil || out != nil {
			t.Fatal(out, err)
		}
	}
	if codec.calls != 0 {
		t.Fatal("encoded before all validation completed")
	}
}
func TestSchemaIsolationAndDefaults(t *testing.T) {
	min := int64(1)
	def := "2"
	fields := []Field{{Key: "n", Kind: Number, Min: &min, Default: &def}}
	e, err := New(fields, nil)
	if err != nil {
		t.Fatal(err)
	}
	min = 100
	def = "bad"
	fields[0].Key = "other"
	if got := e.Resolve(context.Background(), nil)["n"]; got.Value != int64(2) || got.Error != nil {
		t.Fatal(got)
	}
	exposed := e.Fields()
	*exposed[0].Min = 100
	if _, err = e.EncodePatch(context.Background(), map[string]Input{"n": {Value: 3}}); err != nil {
		t.Fatal(err)
	}
	for _, f := range [][]Field{{{Key: "x", Kind: Secret, Default: &def}}, {{Key: "x", Kind: String}, {Key: "x", Kind: String}}, {{Key: "x", Kind: "unsupported"}}, {{Key: "x", Kind: String, Required: true, Clearable: true}}} {
		if _, err = New(f, nil); err == nil {
			t.Fatal("invalid schema accepted")
		}
	}
}
func TestCryptoxCodec(t *testing.T) {
	if _, err := NewCryptoxCodec("short", "enc:"); err == nil {
		t.Fatal("accepted weak key")
	}
	c, err := NewCryptoxCodec(strings.Repeat("k", 32), "enc:")
	if err != nil {
		t.Fatal(err)
	}
	v, err := c.Encrypt(context.Background(), "private")
	if err != nil || !strings.HasPrefix(v, "enc:v1:") {
		t.Fatal(v, err)
	}
	plain, err := c.Decrypt(context.Background(), v)
	if err != nil || plain != "private" {
		t.Fatal(plain, err)
	}
	if _, err = c.Decrypt(context.Background(), "plaintext"); err == nil {
		t.Fatal("accepted unmarked plaintext")
	}
}

func TestInvalidStoredValuesDoNotUseDefaults(t *testing.T) {
	d := "7"
	e, err := New([]Field{{Key: "n", Kind: Number, Default: &d}, {Key: "required", Kind: String, Required: true}, {Key: "secret", Kind: Secret}}, &fakeCodec{})
	if err != nil {
		t.Fatal(err)
	}
	got := e.Resolve(context.Background(), map[string]string{"n": "corrupt", "secret": "encrypted:"})
	if got["n"].State != Invalid || got["n"].Value != nil || got["required"].Error == nil || got["secret"].Configured || got["secret"].State != Invalid {
		t.Fatal(got)
	}
}
func TestNormalizeAndSanitizedCallbackErrors(t *testing.T) {
	e, err := New([]Field{{Key: "origin", Kind: String, Normalize: func(v string) (string, error) { return strings.TrimSuffix(v, "/"), nil }}, {Key: "secret", Kind: Secret, Validate: func(v any) error { return errors.New("secret value: " + v.(string)) }}}, &fakeCodec{})
	if err != nil {
		t.Fatal(err)
	}
	out, err := e.EncodePatch(context.Background(), map[string]Input{"origin": {Value: "https://example.test/"}})
	if err != nil || out[0].Encoded != "https://example.test" {
		t.Fatal(out, err)
	}
	out, err = e.EncodePatch(context.Background(), map[string]Input{"secret": {Value: "private"}})
	if out != nil || !errors.Is(err, ErrValidation) || strings.Contains(err.Error(), "private") {
		t.Fatal(out, err)
	}
}
