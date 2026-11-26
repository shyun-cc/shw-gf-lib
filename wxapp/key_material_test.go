package wxapp

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"testing"
)

func TestGenerateRSAKeyPairInteroperability(t *testing.T) {
	private, public, err := GenerateRSAKeyPair(2048)
	if err != nil {
		t.Fatal(err)
	}
	privateBlock, rest := pem.Decode(private)
	if privateBlock == nil || privateBlock.Type != "PRIVATE KEY" || len(rest) != 0 {
		t.Fatal("expected one PKCS8 PEM")
	}
	publicBlock, rest := pem.Decode(public)
	if publicBlock == nil || publicBlock.Type != "PUBLIC KEY" || len(rest) != 0 {
		t.Fatal("expected one SPKI PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(privateBlock.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok || key.N.BitLen() != 2048 {
		t.Fatal("wrong private key")
	}
	parsedPublic, err := x509.ParsePKIXPublicKey(publicBlock.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	pub, ok := parsedPublic.(*rsa.PublicKey)
	if !ok || pub.N.Cmp(key.N) != 0 || pub.E != key.E {
		t.Fatal("public/private key mismatch")
	}
	data := []byte("https://api.weixin.qq.com/wxa/getuserriskrank\nwx-app\n1635927954\n{}")
	encoded, err := apiSign(key, data)
	if err != nil {
		t.Fatal(err)
	}
	// apiSign's output must verify through independent strict PSS/SHA256 code.
	sig, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(data)
	if rsa.VerifyPSS(pub, crypto.SHA256, hash[:], sig, &rsa.PSSOptions{SaltLength: 32, Hash: crypto.SHA256}) != nil {
		t.Fatal("PSS interoperability failed")
	}
	if rsa.VerifyPSS(pub, crypto.SHA256, hash[:], sig, &rsa.PSSOptions{SaltLength: 20, Hash: crypto.SHA256}) == nil {
		t.Fatal("unexpected PSS salt length")
	}
	// A PKCS#1 v1.5 signature cannot masquerade as the required RSA-PSS format.
	legacy, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hash[:])
	if err != nil {
		t.Fatal(err)
	}
	if rsa.VerifyPSS(pub, crypto.SHA256, hash[:], legacy, nil) == nil {
		t.Fatal("accepted PKCS1v15 signature")
	}
}
func TestGenerateRSAKeyPairInvalidBits(t *testing.T) {
	for _, bits := range []int{-1, 0, 1024, 2049, 8448} {
		private, public, err := GenerateRSAKeyPair(bits)
		if err == nil || private != nil || public != nil {
			t.Errorf("invalid key size %d accepted", bits)
		}
	}
}
