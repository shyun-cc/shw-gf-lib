package wxapp_test

import (
	"fmt"

	"github.com/shyun-cc/shw-gf-lib/wxapp"
)

func ExampleShareSigner_BuildShare() {
	// In production, load the application's registered private key from the
	// project's secret provider. This example generates an ephemeral local key.
	private, _, err := wxapp.GenerateRSAKeyPair(2048)
	if err != nil {
		fmt.Println("key generation failed")
		return
	}
	signer, err := wxapp.NewShareSigner(wxapp.ShareSignerConfig{AppID: "wx-example", SignatureAlgorithm: "RSAwithSHA256", PrivateKeyPEM: private})
	if err != nil {
		fmt.Println("signer configuration failed")
		return
	}
	// Supply original uploaded image bytes. The library never downloads ImageURL;
	// the project must ensure the immutable URL serves these exact bytes.
	share, err := signer.BuildShare(wxapp.ShareInput{Title: "分享标题", Path: "/pages/share/index?id=42", ImageURL: "https://images.example/immutable/share.png", ImageBytes: []byte("example image fixture")})
	if err != nil {
		fmt.Println("share signing failed")
		return
	}
	fmt.Println(share.Title, len(share.ThumbDataHash), share.Signature != "")
	// Return share's JSON fields to onShareAppMessage; keep private keys server-side.
	// Output: 分享标题 64 true
}
