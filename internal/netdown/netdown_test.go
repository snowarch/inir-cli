package netdown

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func response(code int, rawURL, server string) *http.Response {
	u, _ := url.Parse(rawURL)
	h := http.Header{}
	if server != "" {
		h.Set("Server", server)
	}
	return &http.Response{StatusCode: code, Header: h, Request: &http.Request{URL: u}}
}

func TestCause(t *testing.T) {
	for _, c := range []struct {
		code     int
		url, srv string
		want     string
	}{
		{504, "https://github.com/snowarch/inir-cli/releases/download/v1/x", "", "GitHub is having trouble right now (HTTP 504 from github.com)"},
		{502, "https://objects.githubusercontent.com/x", "", "GitHub is having trouble"},
		{503, "https://inir.space/hub/index.json", "GitHub.com", "GitHub is having trouble right now (HTTP 503 from inir.space)"},
		{429, "https://example.org/x", "", "example.org is having trouble right now (HTTP 429)"},
		{404, "https://github.com/x", "", ""},
		{200, "https://github.com/x", "", ""},
	} {
		got := Cause(response(c.code, c.url, c.srv))
		if c.want == "" && got != "" || c.want != "" && !strings.HasPrefix(got, c.want) {
			t.Errorf("%d %s: got %q, want prefix %q", c.code, c.url, got, c.want)
		}
	}
	if Cause(nil) != "" {
		t.Error("nil response named a cause")
	}
}
