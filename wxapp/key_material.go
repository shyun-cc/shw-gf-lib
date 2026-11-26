package wxapp

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
)

// GenerateRSAKeyPair generates local key material for MP API security or share
// verification. It makes no network calls and does not persist or log the keys.
// bits must be a multiple of 256 between 2048 and 8192 inclusive. The private key
// is PKCS#8 PEM; the public key is SubjectPublicKeyInfo PEM. The caller owns safe
// persistence and the MP console upload; never commit private keys to source control.
func GenerateRSAKeyPair(bits int) (privatePEM, publicPEM []byte, err error) {
	if bits < 2048 || bits > 8192 || bits%256 != 0 {
		return nil, nil, securityFailure("keygen")
	}
	key, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		return nil, nil, securityFailure("keygen")
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, securityFailure("keygen")
	}
	publicDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return nil, nil, securityFailure("keygen")
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}), pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER}), nil
}
