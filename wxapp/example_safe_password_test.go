package wxapp_test

import (
	"context"
	"time"

	"github.com/shyun-cc/shw-gf-lib/wxapp"
)

func ExampleSafePasswordService_IssueChallenge() {
	// In production, load the SM2 private key from secret storage. Submit the
	// matching CSR in MP, package the returned certificate in the mini-program,
	// and verify the wire profile against an actual device before enabling it.
	private, _, _, err := wxapp.GenerateSM2KeyAndCSR("wx-example")
	if err != nil {
		return
	}
	store, err := wxapp.NewRedisSafePasswordStore("my-project:production")
	if err != nil {
		return
	}
	service, err := wxapp.NewSafePasswordService(wxapp.SafePasswordConfig{AppID: "wx-example", PrivateKeyPEM: private, Store: store, ChallengeTTL: time.Minute, WireProfile: wxapp.SafePasswordWireProfile{Encoding: "base64", CiphertextFormat: "asn1", HeaderLength: 2}})
	if err != nil {
		return
	}
	// These identifiers come from authenticated server state, never frontend IDs.
	binding := wxapp.SafePasswordBinding{SessionID: "server-session", Purpose: "change-password", CredentialID: "server-credential"}
	// Load the stable profile/version stored alongside the credential. Changing
	// the salt/expression per attempt would prevent subsequent hash comparison.
	profile := wxapp.SafePasswordHashProfile{ID: "pbkdf-v1", Kind: "pbkdf_hmac_hex", HashLength: 64, Salt: "credential-stable-salt"}
	challenge, err := service.IssueChallenge(context.Background(), binding, profile)
	if err != nil {
		return
	}
	_ = challenge // Return only this frontend projection, not a SafePasswordRecord.
	// After receiving the device's encrypted value, call Validate with the same
	// trusted binding and issued nonce. Compare the protected result server-side;
	// never return/log its Hash or treat successful decryption as authentication.
}
