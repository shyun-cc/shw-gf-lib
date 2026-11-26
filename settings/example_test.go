package settings_test

import (
	"context"
	"github.com/shyun-cc/shw-gf-lib/settings"
)

// prepare demonstrates project-owned key configuration without persistence or environment access.
func prepare(ctx context.Context, key string, input map[string]settings.Input) ([]settings.Change, error) {
	codec, err := settings.NewCryptoxCodec(key, "enc:")
	if err != nil {
		return nil, err
	}
	engine, err := settings.New([]settings.Field{{Key: "service.secret", Kind: settings.Secret, Clearable: true}}, codec)
	if err != nil {
		return nil, err
	}
	return engine.EncodePatch(ctx, input)
}
