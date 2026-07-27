package externalaccess

import (
	"net/http"
	"strings"
)

// RewriteTunnelResponsePublicPath preserves the self-hosted relay path prefix
// when the loopback external-access service returns grant-scoped redirects and
// cookies. The relay server applies the same rewrite defensively, so the
// operation remains safe when both tunnel endpoints are on the latest version.
func RewriteTunnelResponsePublicPath(response *TunnelResponseAlias, publicPathPrefix string) {
	if response == nil || len(response.Headers) == 0 {
		return
	}
	publicPathPrefix = normalizeTunnelPublicPathPrefix(publicPathPrefix)
	if publicPathPrefix == "" {
		return
	}
	for key, values := range response.Headers {
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "location":
			rewritten := make([]string, 0, len(values))
			for _, value := range values {
				rewritten = append(rewritten, rewriteTunnelLocation(value, publicPathPrefix))
			}
			response.Headers[key] = rewritten
		case "set-cookie":
			rewritten := make([]string, 0, len(values))
			for _, value := range values {
				rewritten = append(rewritten, rewriteTunnelSetCookiePath(value, publicPathPrefix))
			}
			response.Headers[key] = rewritten
		}
	}
}

func rewriteTunnelLocation(value, publicPathPrefix string) string {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "/g/") {
		return value
	}
	return publicPathPrefix + value
}

func rewriteTunnelSetCookiePath(value, publicPathPrefix string) string {
	parsed := (&http.Response{Header: http.Header{"Set-Cookie": {value}}}).Cookies()
	if len(parsed) == 0 || parsed[0] == nil {
		return value
	}
	cookie := parsed[0]
	if strings.HasPrefix(cookie.Path, "/g/") {
		cookie.Path = publicPathPrefix + cookie.Path
	}
	return cookie.String()
}

func normalizeTunnelPublicPathPrefix(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == "/" {
		return ""
	}
	return "/" + strings.Trim(value, "/")
}
