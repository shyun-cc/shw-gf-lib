// Command wxapp-security prepares and checks local WeChat security materials.
// It never calls WeChat, changes MP settings, or prints private keys.
package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/shyun-cc/shw-gf-lib/wxapp"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: wxapp-security api-keygen|rsa-keygen|sm2-keygen|api-check|share-check|keyboard-check [flags]")
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	directory := flags.String("out", "", "new private output directory")
	appID := flags.String("appid", "", "application ID / CSR common name")
	bits := flags.Int("bits", 2048, "RSA key size")
	configPath := flags.String("config", "", "API configuration JSON file")
	privatePath := flags.String("private-key", "", "private key PEM file")
	certificatePath := flags.String("certificate", "", "keyboard certificate PEM file")
	if flags.Parse(args[1:]) != nil || flags.NArg() != 0 {
		return errors.New("invalid command flags")
	}
	switch args[0] {
	case "rsa-keygen", "api-keygen":
		if *directory == "" {
			return errors.New("out is required")
		}
		private, public, err := wxapp.GenerateRSAKeyPair(*bits)
		if err != nil {
			return err
		}
		materials := map[string][]byte{"private.pem": private, "public.pem": public}
		if args[0] == "api-keygen" {
			key := make([]byte, 32)
			if _, err = rand.Read(key); err != nil {
				return errors.New("cannot generate symmetric key")
			}
			materials["symmetric.key"] = []byte(base64.StdEncoding.EncodeToString(key) + "\n")
		}
		if err = writeMaterials(*directory, materials); err != nil {
			return err
		}
	case "sm2-keygen":
		if *directory == "" || *appID == "" {
			return errors.New("out and appid are required")
		}
		private, public, csr, err := wxapp.GenerateSM2KeyAndCSR(*appID)
		if err != nil {
			return err
		}
		if err = writeMaterials(*directory, map[string][]byte{"private.pem": private, "public.pem": public, "request.csr": csr}); err != nil {
			return err
		}
	case "api-check":
		if err := checkAPI(*configPath); err != nil {
			return err
		}
	case "share-check":
		private, err := readMaterial(*privatePath)
		if err != nil {
			return err
		}
		if _, err = wxapp.NewShareSigner(wxapp.ShareSignerConfig{AppID: *appID, SignatureAlgorithm: "RSAwithSHA256", PrivateKeyPEM: private}); err != nil {
			return err
		}
	case "keyboard-check":
		private, err := readMaterial(*privatePath)
		if err != nil {
			return err
		}
		certificate, err := readMaterial(*certificatePath)
		if err != nil {
			return err
		}
		info, err := wxapp.ValidateSafePasswordCertificate(private, certificate, time.Now())
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "certificate valid until %s; key matches; issuer trust not checked\n", info.NotAfter.UTC().Format(time.RFC3339))
		return err
	default:
		return errors.New("unknown command")
	}
	_, err := fmt.Fprintln(out, "OK (local materials only; WeChat configuration and live behavior not checked)")
	return err
}

func writeMaterials(directory string, materials map[string][]byte) error {
	// Refuse existing directories, symlinks and files. A partially written newly
	// created directory is retained on failure so generated private material is
	// never silently overwritten or removed.
	if os.Mkdir(directory, 0700) != nil {
		return errors.New("cannot create new output directory (existing paths are refused)")
	}
	for name, data := range materials {
		file, err := os.OpenFile(filepath.Join(directory, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return errors.New("cannot create material file; partial output retained")
		}
		_, writeErr := file.Write(data)
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			return errors.New("cannot save material file; partial output retained")
		}
	}
	return nil
}

func readMaterial(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("cannot read material file")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	if err != nil || len(data) == 0 || len(data) > 64<<10 {
		return nil, errors.New("invalid material file size or read failure")
	}
	return data, nil
}

type apiFileConfig struct {
	AppID                string            `json:"appId"`
	EncryptionAlgorithm  string            `json:"encryptionAlgorithm"`
	SignatureAlgorithm   string            `json:"signatureAlgorithm"`
	SymmetricKeyBase64   string            `json:"symmetricKeyBase64"`
	SymmetricKeySerial   string            `json:"symmetricKeySerial"`
	PrivateKeyFile       string            `json:"privateKeyFile"`
	PlatformCertificates map[string]string `json:"platformCertificates"`
}

func checkAPI(path string) error {
	data, err := readMaterial(path)
	if err != nil {
		return err
	}
	var c apiFileConfig
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&c) != nil || decoder.Decode(new(any)) != io.EOF {
		return errors.New("invalid API configuration JSON")
	}
	resolve := func(p string) string {
		if filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(filepath.Dir(path), p)
	}
	private, err := readMaterial(resolve(c.PrivateKeyFile))
	if err != nil {
		return err
	}
	key, err := base64.StdEncoding.Strict().DecodeString(c.SymmetricKeyBase64)
	if err != nil {
		return errors.New("invalid symmetric key encoding")
	}
	certificates := make(map[string][]byte, len(c.PlatformCertificates))
	for id, file := range c.PlatformCertificates {
		certificates[id], err = readMaterial(resolve(file))
		if err != nil {
			return err
		}
	}
	_, err = wxapp.NewWxAppService(c.AppID, "").WithAPISecurity(wxapp.APISecurityConfig{
		EncryptionAlgorithm: c.EncryptionAlgorithm, SignatureAlgorithm: c.SignatureAlgorithm,
		SymmetricKey: key, SymmetricKeySerial: c.SymmetricKeySerial, PrivateKeyPEM: private,
		PlatformCertificates: certificates,
	})
	return err
}
