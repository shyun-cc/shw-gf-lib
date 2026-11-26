package wxapp

import (
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/url"
	"strings"
	"unicode/utf8"
)

// ShareSignerConfig supplies the MP application's registered signing key. Only
// RSAwithSHA256 is currently supported (RSA-PSS/SHA256, salt length 32). Key PEM
// is parsed into an independent snapshot; no credentials are stored globally.
type ShareSignerConfig struct {
	AppID              string
	SignatureAlgorithm string
	PrivateKeyPEM      []byte
}

// ShareSigner creates signed onShareAppMessage configurations without network
// access. Its private configuration is immutable and safe for concurrent use.
// Obtain a new signer to rotate the application ID or private key.
type ShareSigner struct {
	appID      string
	privateKey *rsa.PrivateKey
}

// ShareInput contains exact sharing values plus the image's original bytes.
// ImageBytes must match the bytes served by ImageURL when WeChat downloads it.
// The caller owns image validation and stable publication at the chosen URL.
type ShareInput struct {
	Title      string
	Path       string
	ImageURL   string
	ImageBytes []byte
}

// ShareHashInput accepts a precomputed lower-case hexadecimal SHA256 image hash.
// Use it when image bytes were already hashed by a trusted upload pipeline.
type ShareHashInput struct {
	Title         string
	Path          string
	ImageURL      string
	ThumbDataHash string
}

// ShareConfig is the complete five-field payload for onShareAppMessage.
// The signature binds appid, path, thumbDataHash and title, not imageUrl itself.
// A different URL serving identical image bytes has the same signed image hash.
type ShareConfig struct {
	Title         string `json:"title"`
	Path          string `json:"path"`
	ImageURL      string `json:"imageUrl"`
	ThumbDataHash string `json:"thumbDataHash"`
	Signature     string `json:"signature"`
}

// NewShareSigner constructs a local signer from PKCS#8 or PKCS#1 RSA PEM. The
// corresponding public key must be registered separately in the MP console.
func NewShareSigner(c ShareSignerConfig) (*ShareSigner, error) {
	if c.AppID == "" || !utf8.ValidString(c.AppID) || c.SignatureAlgorithm != "RSAwithSHA256" {
		return nil, securityFailure("share_config")
	}
	key, err := parseAPIRSAKey(c.PrivateKeyPEM)
	if err != nil {
		return nil, securityFailure("share_config")
	}
	return &ShareSigner{appID: c.AppID, privateKey: key}, nil
}

// ShareImageHash returns the SHA256 digest of the exact image bytes as lower-case
// hexadecimal. Empty input is rejected; this helper does not decode images.
func ShareImageHash(image []byte) (string, error) {
	if len(image) == 0 {
		return "", securityFailure("share_hash")
	}
	digest := sha256.Sum256(image)
	return hex.EncodeToString(digest[:]), nil
}

// BuildShare hashes the supplied image bytes and signs the exact title/path.
// It never downloads ImageURL or normalizes/decodes the supplied path and query.
func (s *ShareSigner) BuildShare(input ShareInput) (*ShareConfig, error) {
	hash, err := ShareImageHash(input.ImageBytes)
	if err != nil {
		return nil, err
	}
	return s.BuildShareWithHash(ShareHashInput{Title: input.Title, Path: input.Path, ImageURL: input.ImageURL, ThumbDataHash: hash})
}

// BuildShareWithHash signs a caller-provided image hash. ImageURL must be an HTTPS
// URL with a hostname and no userinfo or fragment. The caller must ensure that
// the URL serves bytes matching ThumbDataHash; this function performs no I/O.
func (s *ShareSigner) BuildShareWithHash(input ShareHashInput) (*ShareConfig, error) {
	if s == nil || s.privateKey == nil || s.appID == "" {
		return nil, securityFailure("share_config")
	}
	if input.Title == "" || !utf8.ValidString(input.Title) || !strings.HasPrefix(input.Path, "/") || !utf8.ValidString(input.Path) || !utf8.ValidString(input.ImageURL) {
		return nil, securityFailure("share_input")
	}
	imageURL, err := url.Parse(input.ImageURL)
	if err != nil || imageURL.Scheme != "https" || imageURL.Hostname() == "" || imageURL.User != nil || imageURL.Fragment != "" || strings.Contains(input.ImageURL, "#") {
		return nil, securityFailure("share_input")
	}
	if len(input.ThumbDataHash) != 64 {
		return nil, securityFailure("share_hash")
	}
	for _, c := range input.ThumbDataHash {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return nil, securityFailure("share_hash")
		}
	}
	signature, err := apiSign(s.privateKey, shareSignatureData(s.appID, input.Path, input.ThumbDataHash, input.Title))
	if err != nil {
		return nil, securityFailure("share_sign")
	}
	return &ShareConfig{Title: input.Title, Path: input.Path, ImageURL: input.ImageURL, ThumbDataHash: input.ThumbDataHash, Signature: signature}, nil
}

func shareSignatureData(appID, path, hash, title string) []byte {
	// Official ASCII order is fixed. Values are UTF-8 bytes encoded with standard
	// Base64, not URL encoding/Base64URL. There is no trailing newline.
	encode := base64.StdEncoding.EncodeToString
	return []byte("appid=" + encode([]byte(appID)) + "&path=" + encode([]byte(path)) + "&thumbDataHash=" + encode([]byte(hash)) + "&title=" + encode([]byte(title)))
}
