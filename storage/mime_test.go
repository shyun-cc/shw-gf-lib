package storage

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestUploadTicketMediaContentType(t *testing.T) {
	cases := []struct {
		filename string
		want     string
	}{
		{"clip.mp4", "video/mp4"},
		{"clip.mov", "video/quicktime"},
		{"clip.m4v", "video/x-m4v"},
		{"clip.webm", "video/webm"},
		{"sound.mp3", "audio/mpeg"},
		{"unknown.bin", "application/octet-stream"},
	}

	oss := newTestOss()
	s3 := NewS3("AK", "SK", "bucket1", "http://127.0.0.1:9000")
	for _, tc := range cases {
		t.Run(tc.filename, func(t *testing.T) {
			ossTicket, err := oss.UploadTicket(context.Background(), "temp", tc.filename, 600, 0, 0)
			if err != nil {
				t.Fatal(err)
			}
			if ossTicket.ContentType != tc.want || ossTicket.FormData["x-oss-content-type"] != tc.want {
				t.Fatalf("OSS content type: ticket=%q form=%q, want %q", ossTicket.ContentType, ossTicket.FormData["x-oss-content-type"], tc.want)
			}

			policyJSON, err := base64.StdEncoding.DecodeString(ossTicket.FormData["policy"])
			if err != nil {
				t.Fatal(err)
			}
			var policy struct {
				Conditions []any `json:"conditions"`
			}
			if err := json.Unmarshal(policyJSON, &policy); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, condition := range policy.Conditions {
				parts, ok := condition.([]any)
				if ok && len(parts) == 3 && parts[0] == "eq" && parts[1] == "$x-oss-content-type" && parts[2] == tc.want {
					found = true
				}
			}
			if !found {
				t.Fatalf("OSS policy missing content type condition %q: %s", tc.want, policyJSON)
			}

			s3Ticket, err := s3.UploadTicket(context.Background(), "temp", tc.filename, 600, 0, 0)
			if err != nil {
				t.Fatal(err)
			}
			if s3Ticket.ContentType != tc.want {
				t.Fatalf("S3 content type=%q, want %q", s3Ticket.ContentType, tc.want)
			}
		})
	}
}
