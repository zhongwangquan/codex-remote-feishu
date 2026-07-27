package main

import (
	"strings"
	"testing"

	"github.com/kxn/codex-remote-feishu/internal/externalaccess"
)

func TestRewriteExternalAccessHeadersPreservesRelayPrefix(t *testing.T) {
	response := &externalaccess.TunnelResponseAlias{
		Headers: map[string][]string{
			"Location":   {"/g/grant-1/preview-1"},
			"Set-Cookie": {"codex_preview_session=value; Path=/g/grant-1/; HttpOnly"},
		},
	}

	rewriteExternalAccessHeaders(response, "/codex-preview", "jason-local")

	if got := response.Headers["Location"][0]; got != "/codex-preview/t/jason-local/g/grant-1/preview-1" {
		t.Fatalf("location = %q", got)
	}
	if got := response.Headers["Set-Cookie"][0]; !strings.Contains(got, "Path=/codex-preview/t/jason-local/g/grant-1/") {
		t.Fatalf("cookie path was not rewritten: %q", got)
	}
}
