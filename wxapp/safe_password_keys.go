package wxapp

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/emmansun/gmsm/sm2"
	"github.com/emmansun/gmsm/smx509"
)

// SafePasswordCertificateInfo contains public certificate metadata. It does not
// assert Tencent issuance, AppID authorization, certificate trust or revocation.
type SafePasswordCertificateInfo struct {
	CommonName string
	NotBefore  time.Time
	NotAfter   time.Time
}

// GenerateSM2KeyAndCSR generates an SM2 key pair and signed PKCS#10 request
// entirely locally. Outputs are unencrypted PKCS#8 PRIVATE KEY, SPKI PUBLIC KEY
// and CERTIFICATE REQUEST PEM, respectively. Nothing is persisted or logged.
// commonName is normally the application's AppID; it must be valid UTF-8,
// nonempty, at most 64 bytes, with no control characters or surrounding spaces.
// Submit the CSR in MP and keep the private key in server-side secret storage.
func GenerateSM2KeyAndCSR(commonName string) (privatePEM, publicPEM, csrPEM []byte, err error) {
	if commonName == "" || len(commonName) > 64 || !utf8.ValidString(commonName) || strings.TrimSpace(commonName) != commonName || strings.ContainsFunc(commonName, unicode.IsControl) {
		return nil, nil, nil, securityFailure("keyboard_csr")
	}
	key, err := sm2.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, nil, securityFailure("keyboard_key")
	}
	privateDER, err := smx509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, nil, securityFailure("keyboard_key")
	}
	publicDER, err := smx509.MarshalPKIXPublicKey(key.Public())
	if err != nil {
		return nil, nil, nil, securityFailure("keyboard_key")
	}
	csrDER, err := smx509.CreateCertificateRequest(rand.Reader, &smx509.CertificateRequest{Subject: pkix.Name{CommonName: commonName}, SignatureAlgorithm: smx509.SM2WithSM3}, key)
	if err != nil {
		return nil, nil, nil, securityFailure("keyboard_csr")
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}), pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER}), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER}), nil
}

// parseSafePasswordPrivateKey accepts exactly one unencrypted PKCS#8 SM2 PEM.
// Raw hexadecimal keys and other PEM private-key formats are not accepted.
func parseSafePasswordPrivateKey(data []byte) (*sm2.PrivateKey, error) {
	data = bytes.TrimSpace(data)
	block, rest := pem.Decode(data)
	if block == nil || block.Type != "PRIVATE KEY" || len(block.Headers) != 0 || len(bytes.TrimSpace(rest)) != 0 || !bytes.HasPrefix(data, []byte("-----BEGIN PRIVATE KEY-----")) {
		return nil, securityFailure("keyboard_key")
	}
	var value asn1.RawValue
	if rest, err := asn1.Unmarshal(block.Bytes, &value); err != nil || len(rest) != 0 {
		return nil, securityFailure("keyboard_key")
	}
	parsed, err := smx509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, securityFailure("keyboard_key")
	}
	key, ok := parsed.(*sm2.PrivateKey)
	if !ok {
		return nil, securityFailure("keyboard_key")
	}
	return key, nil
}

// ValidateSafePasswordCertificate checks SM2 material, matching public keys and
// certificate validity at the caller-supplied nonzero time. It does not verify
// the issuer, chain, revocation or AppID binding: obtain the certificate from MP
// using the generated CSR. Successful validation is not proof of Tencent trust.
func ValidateSafePasswordCertificate(privatePEM, certPEM []byte, now time.Time) (*SafePasswordCertificateInfo, error) {
	key, err := parseSafePasswordPrivateKey(privatePEM)
	if err != nil {
		return nil, err
	}
	certPEM = bytes.TrimSpace(certPEM)
	block, rest := pem.Decode(certPEM)
	if now.IsZero() || block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 || len(bytes.TrimSpace(rest)) != 0 || !bytes.HasPrefix(certPEM, []byte("-----BEGIN CERTIFICATE-----")) {
		return nil, securityFailure("keyboard_certificate")
	}
	cert, err := smx509.ParseCertificate(block.Bytes)
	if err != nil || now.Before(cert.NotBefore) || now.After(cert.NotAfter) {
		return nil, securityFailure("keyboard_certificate")
	}
	public, ok := cert.PublicKey.(*ecdsa.PublicKey)
	if !ok || public.Curve != sm2.P256() || !public.Equal(key.Public()) {
		return nil, securityFailure("keyboard_certificate")
	}
	return &SafePasswordCertificateInfo{CommonName: cert.Subject.CommonName, NotBefore: cert.NotBefore, NotAfter: cert.NotAfter}, nil
}
