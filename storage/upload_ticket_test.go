package storage

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestUploadTicketExactKey(t *testing.T) {
	for name, store := range map[string]Storage{"oss": newTestOss(), "s3": NewS3("AK", "SK", "bucket", "s3.example.com")} {
		t.Run(name, func(t *testing.T) {
			ticket, err := store.UploadTicket(context.Background(), "temp/", "a.png", 600, 1, 1024)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := base64.StdEncoding.DecodeString(ticket.FormData["policy"])
			if err != nil {
				t.Fatal(err)
			}
			var policy struct {
				Conditions []json.RawMessage `json:"conditions"`
				Expiration string            `json:"expiration"`
			}
			if err := json.Unmarshal(raw, &policy); err != nil {
				t.Fatal(err)
			}
			var fixedKey, size bool
			for _, raw := range policy.Conditions {
				var condition []any
				if json.Unmarshal(raw, &condition) != nil {
					continue
				}
				if reflect.DeepEqual(condition, []any{"eq", "$key", "temp/a.png"}) {
					fixedKey = true
				}
				if reflect.DeepEqual(condition, []any{"content-length-range", float64(1), float64(1024)}) {
					size = true
				}
				if len(condition) > 1 && condition[0] == "starts-with" && condition[1] == "$key" {
					t.Fatal("prefix write authorization remains")
				}
			}
			if !fixedKey || !size || policy.Expiration == "" || ticket.FormData["key"] != "temp/a.png" {
				t.Fatal("ticket constraints missing")
			}
			if strings.Contains(string(raw), "forbid-overwrite") || ticket.FormData["x-oss-forbid-overwrite"] != "" {
				t.Fatal("same-key replay forbidden")
			}

		})
	}
}

func TestUploadTicketLiteralKeyValidation(t *testing.T) {
	for _, store := range []Storage{newTestOss(), NewS3("AK", "SK", "bucket", "s3.example.com")} {
		for _, input := range [][2]string{{"temp", ""}, {"temp", "${filename}"}, {"temp/${name}", "a.png"}, {"temp", string([]byte{0xff})}} {
			if ticket, err := store.UploadTicket(context.Background(), input[0], input[1], 600, 0, 0); err == nil || ticket != nil {
				t.Fatal("nonliteral key accepted")
			}
		}
	}
}

func TestOssEmptySubresourceSignatureIndependent(t *testing.T) {
	o := newTestOss()
	req, err := o.signedRequest(context.Background(), "GET", "", nil, map[string]string{"versioning": ""}, nil)
	if err != nil {
		t.Fatal(err)
	}
	h := func(k []byte, v string) []byte {
		mac := hmac.New(sha256.New, k)
		mac.Write([]byte(v))
		return mac.Sum(nil)
	}
	canonical := "GET\n/bucket1/\nversioning\nhost:bucket1.oss-cn-hangzhou.aliyuncs.com\nx-oss-content-sha256:UNSIGNED-PAYLOAD\nx-oss-date:20260818T120000Z\n\nhost;x-oss-content-sha256;x-oss-date\nUNSIGNED-PAYLOAD"
	digest := sha256.Sum256([]byte(canonical))
	sts := "OSS4-HMAC-SHA256\n20260818T120000Z\n20260818/cn-hangzhou/oss/aliyun_v4_request\n" + hex.EncodeToString(digest[:])
	key := []byte("aliyun_v4SK_TEST")
	for _, value := range []string{"20260818", "cn-hangzhou", "oss", "aliyun_v4_request"} {
		key = h(key, value)
	}
	if !strings.HasSuffix(req.Header.Get("Authorization"), "Signature="+hex.EncodeToString(h(key, sts))) {
		t.Fatal("empty subresource canonical signature mismatch")
	}
	if got := ossCanonicalQuery(map[string]string{"z": "", "a": "a=b"}); got != "a=a%3Db&z" {
		t.Fatal("OSS empty query encoding mismatch")
	}
	if got := canonicalQuery(map[string]string{"versioning": ""}); got != "versioning=" {
		t.Fatal("AWS empty query encoding changed")
	}
}
