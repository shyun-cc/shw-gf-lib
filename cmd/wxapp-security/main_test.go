package main

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalMaterialWriterRefusesOverwriteAndKeepsPrivatePermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "materials")
	if err := writeMaterials(dir, map[string][]byte{"private.pem": []byte("private-fixture")}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "private.pem"))
	if err != nil || info.Mode().Perm()&0077 != 0 {
		t.Fatal("private file is accessible to group/others")
	}
	if err = writeMaterials(dir, map[string][]byte{"private.pem": []byte("replaced")}); err == nil {
		t.Fatal("existing directory accepted")
	}
	got, _ := os.ReadFile(filepath.Join(dir, "private.pem"))
	if string(got) != "private-fixture" {
		t.Fatal("existing material changed")
	}
}

func TestCommandErrorsDoNotEchoSecretPathsOrValues(t *testing.T) {
	for _, args := range [][]string{{"unknown"}, {"share-check", "-appid", "wx", "-private-key", "/private-fixture-not-found"}, {"api-check", "-config", "/secret-fixture-not-found"}, {"sm2-keygen"}} {
		var out bytes.Buffer
		err := run(args, &out)
		if err == nil || strings.Contains(err.Error(), "fixture") || out.Len() != 0 {
			t.Fatal("unsafe or missing command failure")
		}
	}
}

func TestAPIKeygenWritesMaterialsWithoutPrintingSecrets(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "api-materials")
	var out bytes.Buffer
	if err := run([]string{"api-keygen", "-out", dir}, &out); err != nil {
		t.Fatal(err)
	}
	keyText, err := os.ReadFile(filepath.Join(dir, "symmetric.key"))
	if err != nil {
		t.Fatal(err)
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(keyText)))
	if err != nil || len(key) != 32 {
		t.Fatal("invalid AES256 material")
	}
	private, err := os.ReadFile(filepath.Join(dir, "private.pem"))
	if err != nil || len(private) == 0 || strings.Contains(out.String(), "PRIVATE KEY") || strings.Contains(out.String(), strings.TrimSpace(string(keyText))) {
		t.Fatal("missing or exposed private material")
	}
	if err = run([]string{"api-keygen", "-out", dir}, &out); err == nil {
		t.Fatal("existing materials accepted")
	}
}
