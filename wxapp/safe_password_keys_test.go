package wxapp

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/emmansun/gmsm/sm2"
	"github.com/emmansun/gmsm/smx509"
)

func TestGenerateSM2KeyAndCSRMaterials(t *testing.T) {
	private, public, request, err := GenerateSM2KeyAndCSR("wx-test-app")
	if err != nil {
		t.Fatal(err)
	}
	privateBlock, privateRest := pem.Decode(private)
	publicBlock, publicRest := pem.Decode(public)
	requestBlock, requestRest := pem.Decode(request)
	if privateBlock == nil || privateBlock.Type != "PRIVATE KEY" || len(privateRest) != 0 || publicBlock == nil || publicBlock.Type != "PUBLIC KEY" || len(publicRest) != 0 || requestBlock == nil || requestBlock.Type != "CERTIFICATE REQUEST" || len(requestRest) != 0 {
		t.Fatal("unexpected generated PEM formats")
	}
	parsedPrivate, err := smx509.ParsePKCS8PrivateKey(privateBlock.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	key, ok := parsedPrivate.(*sm2.PrivateKey)
	if !ok {
		t.Fatal("not an SM2 private key")
	}
	parsedPublic, err := smx509.ParsePKIXPublicKey(publicBlock.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	pub, ok := parsedPublic.(*ecdsa.PublicKey)
	if !ok || pub.Curve != sm2.P256() || !pub.Equal(key.Public()) {
		t.Fatal("SPKI key mismatch")
	}
	csr, err := smx509.ParseCertificateRequest(requestBlock.Bytes)
	if err != nil || csr.CheckSignature() != nil || csr.Subject.CommonName != "wx-test-app" || csr.SignatureAlgorithm != smx509.SM2WithSM3 {
		t.Fatal("CSR subject or signature invalid")
	}
	csrPublic, ok := csr.PublicKey.(*ecdsa.PublicKey)
	if !ok || !csrPublic.Equal(key.Public()) {
		t.Fatal("CSR public key mismatch")
	}
	// Independently parsed keys must interoperate with the SM2 cipher primitive.
	plain := []byte("offline SM2 key interoperability")
	ciphertext, err := sm2.Encrypt(rand.Reader, pub, plain, sm2.ASN1EncrypterOpts)
	if err != nil {
		t.Fatal(err)
	}
	decrypted, err := key.Decrypt(nil, ciphertext, sm2.ASN1DecrypterOpts)
	if err != nil || !bytes.Equal(decrypted, plain) {
		t.Fatal("SM2 key encryption/decryption mismatch")
	}
	second, _, _, err := GenerateSM2KeyAndCSR("wx-test-app")
	if err != nil || bytes.Equal(private, second) {
		t.Fatal("key generation did not produce independent material")
	}
}

func TestGenerateSM2KeyAndCSRRejectsInvalidSubject(t *testing.T) {
	for _, name := range []string{"", " wx-app", "wx-app ", "wx\napp", "wx\x00app", strings.Repeat("a", 65), string([]byte{0xff})} {
		private, public, csr, err := GenerateSM2KeyAndCSR(name)
		if err == nil || private != nil || public != nil || csr != nil {
			t.Fatal("invalid CSR name accepted or partial keys returned")
		}
		assertSafePasswordMaterialError(t, err)
	}
}

func safePasswordFixtureCertificate(t *testing.T, key *sm2.PrivateKey, before, after time.Time) []byte {
	t.Helper()
	template := &smx509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "public-fixture-name"}, NotBefore: before, NotAfter: after, SignatureAlgorithm: smx509.SM2WithSM3, KeyUsage: smx509.KeyUsageKeyEncipherment}
	der, err := smx509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func TestValidateSafePasswordCertificateMatchAndValidity(t *testing.T) {
	private, _, _, err := GenerateSM2KeyAndCSR("wx-app")
	if err != nil {
		t.Fatal(err)
	}
	key, err := parseSafePasswordPrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	before, after := now.Add(-time.Hour), now.Add(time.Hour)
	certificate := safePasswordFixtureCertificate(t, key, before, after)
	// A self-signed certificate intentionally succeeds: the helper validates
	// matching material and time, and makes no Tencent issuance/trust claim.
	for _, at := range []time.Time{before, now, after} {
		info, err := ValidateSafePasswordCertificate(private, certificate, at)
		if err != nil || info == nil || info.CommonName != "public-fixture-name" || !info.NotBefore.Equal(before) || !info.NotAfter.Equal(after) {
			t.Fatal("matching certificate or public metadata rejected")
		}
	}
	other, _, _, err := GenerateSM2KeyAndCSR("wx-other")
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []struct {
		private, cert []byte
		at            time.Time
	}{
		{other, certificate, now},
		{private, certificate, before.Add(-time.Second)},
		{private, certificate, after.Add(time.Second)},
		{private, certificate, time.Time{}},
		{private, []byte("secret-invalid-certificate"), now},
		{private, append(bytes.Clone(certificate), certificate...), now},
		{[]byte("secret-invalid-key"), certificate, now},
	} {
		info, err := ValidateSafePasswordCertificate(candidate.private, candidate.cert, candidate.at)
		if err == nil || info != nil {
			t.Fatal("invalid certificate material accepted")
		}
		assertSafePasswordMaterialError(t, err)
	}
}

func TestParseSafePasswordPrivateKeyRejectsOtherMaterial(t *testing.T) {
	private, public, csr, err := GenerateSM2KeyAndCSR("wx-app")
	if err != nil {
		t.Fatal(err)
	}
	rsaPrivate, _, err := GenerateRSAKeyPair(2048)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(private)
	trailingDER := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: append(bytes.Clone(block.Bytes), 0)})
	headerPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Headers: map[string]string{"Comment": "secret-invalid-key"}, Bytes: block.Bytes})
	for _, material := range [][]byte{nil, []byte("secret-invalid-key"), []byte(strings.Repeat("a", 64)), public, csr, rsaPrivate, append(bytes.Clone(private), private...), trailingDER, headerPEM, append([]byte("secret-preamble\n"), private...)} {
		key, err := parseSafePasswordPrivateKey(material)
		if err == nil || key != nil {
			t.Fatal("non-PKCS8 SM2 key accepted")
		}
		assertSafePasswordMaterialError(t, err)
	}
}

func TestValidateSafePasswordCertificateRejectsRSA(t *testing.T) {
	private, _, _, err := GenerateSM2KeyAndCSR("wx-app")
	if err != nil {
		t.Fatal(err)
	}
	fixture := newAPISecurityFixture(t)
	info, err := ValidateSafePasswordCertificate(private, fixture.config.PlatformCertificates["platform-id-not-x509-7"], time.Now())
	if err == nil || info != nil {
		t.Fatal("RSA certificate accepted for SM2")
	}
	assertSafePasswordMaterialError(t, err)
}

func assertSafePasswordMaterialError(t *testing.T, err error) {
	t.Helper()
	var safe *SecurityError
	if !errors.As(err, &safe) {
		t.Fatalf("expected safe material error, got %T", err)
	}
	for _, secret := range []string{"secret-invalid-key", "secret-invalid-certificate", "PRIVATE KEY", "secret-preamble"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatal("material error leaked input")
		}
	}
}
