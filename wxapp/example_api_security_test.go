package wxapp_test

import (
	"encoding/base64"
	"os"

	"github.com/shyun-cc/shw-gf-lib/wxapp"
)

// Compile-time source compatibility for existing positional initializers.
var _ = wxapp.WxAppService{"appid", "secret", nil, ""}

// Each application reads its own materials from the business secret store.
func ExampleWxAppService_WithAPISecurity() {
	privatePEM, err := os.ReadFile("/run/secrets/wechat-api-private.pem")
	if err != nil {
		return
	}
	certificatePEM, err := os.ReadFile("/run/secrets/wechat-platform.pem")
	if err != nil {
		return
	}
	key, err := base64.StdEncoding.DecodeString(os.Getenv("WECHAT_API_SYMMETRIC_KEY"))
	if err != nil {
		return
	}
	service, err := wxapp.NewWxAppService(os.Getenv("WECHAT_APPID"), os.Getenv("WECHAT_APPSECRET")).WithAPISecurity(wxapp.APISecurityConfig{
		EncryptionAlgorithm: "AES256_GCM",
		SignatureAlgorithm:  "RSAwithSHA256",
		SymmetricKey:        key,
		SymmetricKeySerial:  os.Getenv("WECHAT_API_SYMMETRIC_SERIAL"),
		PrivateKeyPEM:       privatePEM,
		PlatformCertificates: map[string][]byte{
			os.Getenv("WECHAT_PLATFORM_CERTIFICATE_ID"): certificatePEM,
		},
	})
	if err != nil {
		return // Handle configuration failure before accepting business traffic.
	}
	_ = service // Existing GetPhoneNumber/GetUserRiskRank calls use this instance.
}
