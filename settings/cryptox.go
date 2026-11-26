package settings

import (
	"context"
	"github.com/shyun-cc/shw-gf-lib/cryptox"
	"strings"
)

type cryptoxCodec struct{ key, prefix string }

// NewCryptoxCodec uses only cryptox's strong-key path; it never reads environment variables.
// Prefix is supplied by the project, e.g. enc:. Plaintext legacy values are not accepted.
func NewCryptoxCodec(key, prefix string) (Codec, error) {
	if len(key) < 32 {
		return nil, ErrCodec
	}
	return &cryptoxCodec{key, prefix}, nil
}
func (c *cryptoxCodec) Encrypt(ctx context.Context, value string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	v, err := cryptox.Encrypt(value, c.key)
	if err != nil {
		return "", ErrCodec
	}
	return c.prefix + v, nil
}
func (c *cryptoxCodec) Decrypt(ctx context.Context, value string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !strings.HasPrefix(value, c.prefix) {
		return "", ErrCodec
	}
	v, err := cryptox.Decrypt(strings.TrimPrefix(value, c.prefix), c.key)
	if err != nil {
		return "", ErrCodec
	}
	return v, nil
}
