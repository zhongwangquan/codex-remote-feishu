package externalaccess

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleRelayRequestPreservesPublicGrantPath(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{
			Name:     "codex_preview_session",
			Value:    "session-token",
			Path:     "/g/grant-1/",
			HttpOnly: true,
		})
		w.Header().Set("Location", "/g/grant-1/preview-1")
		w.WriteHeader(http.StatusFound)
	}))
	defer upstream.Close()

	client := &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	response := handleRelayRequest(client, upstream.URL, &tunnelRequest{
		ID:     "request-1",
		Method: http.MethodGet,
		Path:   "/g/grant-1/?t=one-time-token",
	})
	RewriteTunnelResponsePublicPath(response, "/codex-preview/t/jason-local")

	if response.Status != http.StatusFound {
		t.Fatalf("status = %d, want 302", response.Status)
	}
	if got := firstHeaderValue(response.Headers, "Location"); got != "/codex-preview/t/jason-local/g/grant-1/preview-1" {
		t.Fatalf("location = %q", got)
	}
	cookie := firstHeaderValue(response.Headers, "Set-Cookie")
	if !strings.Contains(cookie, "Path=/codex-preview/t/jason-local/g/grant-1/") {
		t.Fatalf("cookie path was not rewritten: %q", cookie)
	}
}

func TestRewriteTunnelResponsePublicPathLeavesUnrelatedHeadersUntouched(t *testing.T) {
	response := &TunnelResponseAlias{
		Headers: map[string][]string{
			"Location":   {"/login"},
			"Set-Cookie": {"other=value; Path=/"},
		},
	}

	RewriteTunnelResponsePublicPath(response, "/codex-preview/t/jason-local")

	if got := firstHeaderValue(response.Headers, "Location"); got != "/login" {
		t.Fatalf("location = %q, want /login", got)
	}
	if got := firstHeaderValue(response.Headers, "Set-Cookie"); !strings.Contains(got, "Path=/") {
		t.Fatalf("cookie changed unexpectedly: %q", got)
	}
}

func TestSelfHostedRelayPublicPathIncludesConfiguredBasePath(t *testing.T) {
	if got := selfHostedRelayPublicPath("https://api.flutterweb.cn/codex-preview", "jason-local"); got != "/codex-preview/t/jason-local" {
		t.Fatalf("public path = %q", got)
	}
}

func firstHeaderValue(headers map[string][]string, key string) string {
	for current, values := range headers {
		if strings.EqualFold(current, key) && len(values) != 0 {
			return values[0]
		}
	}
	return ""
}
