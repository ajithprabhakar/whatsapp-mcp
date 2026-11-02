package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestMediaDirectPathPreservesSignedQuery(t *testing.T) {
	cases := []struct{ name, input, want string }{
		{"signed", "https://mmg.whatsapp.net/v/file.enc?ccb=11-4&oh=SIGNATURE&oe=EXPIRY&mms3=true", "/v/file.enc?ccb=11-4&oh=SIGNATURE&oe=EXPIRY&mms3=true"},
		{"escaped", "https://mmg.whatsapp.net/v/a%2Fb.enc?oh=a%2Bb%2Fc%3D&oe=ABC", "/v/a%2Fb.enc?oh=a%2Bb%2Fc%3D&oe=ABC"},
		{"duplicate-provider-key", "https://mmg.whatsapp.net/v/file.enc?ccb=11-4&ccb=other&oh=SIGNED", "/v/file.enc?ccb=11-4&ccb=other&oh=SIGNED"},
		{"queryless-compatibility", "https://mmg.whatsapp.net/v/file.enc", "/v/file.enc"},
		{"direct-path", "/v/file.enc?ccb=11-4&oh=SIGNED", "/v/file.enc?ccb=11-4&oh=SIGNED"},
		{"unsupported-host-fallback", "https://example.invalid/file.enc?oh=SIGNED", "https://example.invalid/file.enc?oh=SIGNED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractDirectPathFromURL(tc.input); got != tc.want {
				t.Fatalf("direct path = %q; want %q", got, tc.want)
			}
		})
	}
}

func TestSignedMediaPathSurvivesWhatsmeowQueryAppend(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("oh") != "SIGNED+/=" || q.Get("oe") != "EXPIRY" || len(q["hash"]) != 1 || q.Get("hash") != "ENCRYPTED_HASH" || len(q["mms-type"]) != 1 || q.Get("mms-type") != "document" {
			http.Error(w, "signature rejected", http.StatusForbidden)
			return
		}
		io.WriteString(w, "encrypted fixture")
	}))
	defer server.Close()
	signed := "https://mmg.whatsapp.net/v/file.enc?ccb=11-4&oh=SIGNED%2B%2F%3D&oe=EXPIRY"
	path := extractDirectPathFromURL(signed)
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		t.Fatalf("expected host-free provider path, got %q", path)
	}
	// Pinned whatsmeow DownloadMediaWithPath appends these derived parameters.
	requestURL := server.URL + path + "&hash=" + url.QueryEscape("ENCRYPTED_HASH") + "&mms-type=document&__wa-mms="
	response, err := server.Client().Get(requestURL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("signed download returned %d, want 200", response.StatusCode)
	}
}
