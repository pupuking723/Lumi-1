package http

import (
	"net/http"
	"os"
	"strings"
)

func liveRequestWithBrowserAuth(r *http.Request) (*http.Request, string) {
	bearer := extractBearerToken(r)
	if bearer == "" {
		bearer = strings.TrimSpace(r.URL.Query().Get("token"))
	}
	if bearer == "" {
		bearer = liveCookieValue(r, liveCookieNames(
			[]string{"GOCLAW_LIVE_TOKEN_COOKIE_NAME", "LUMI_LIVE_TOKEN_COOKIE_NAME"},
			"lumi_live_token",
		)...)
	}

	headers := r.Header.Clone()
	changed := false
	if headers.Get("X-GoClaw-User-Id") == "" {
		if userID := liveCookieValue(r, liveCookieNames(
			[]string{"GOCLAW_LIVE_USER_COOKIE_NAME", "LUMI_LIVE_USER_COOKIE_NAME"},
			"lumi_live_user_id",
		)...); userID != "" {
			headers.Set("X-GoClaw-User-Id", userID)
			changed = true
		}
	}
	if headers.Get("X-GoClaw-Tenant-Id") == "" {
		if tenantID := liveCookieValue(r, liveCookieNames(
			[]string{"GOCLAW_LIVE_TENANT_COOKIE_NAME", "LUMI_LIVE_TENANT_COOKIE_NAME"},
			"lumi_live_tenant_id",
		)...); tenantID != "" {
			headers.Set("X-GoClaw-Tenant-Id", tenantID)
			changed = true
		}
	}

	if !changed {
		return r, bearer
	}
	req := r.Clone(r.Context())
	req.Header = headers
	return req, bearer
}

func liveCookieNames(envKeys []string, defaults ...string) []string {
	names := make([]string, 0, len(envKeys)+len(defaults))
	for _, key := range envKeys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			names = append(names, value)
		}
	}
	return append(names, defaults...)
}

func liveCookieValue(r *http.Request, names ...string) string {
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		cookie, err := r.Cookie(name)
		if err == nil {
			return strings.TrimSpace(cookie.Value)
		}
	}
	return ""
}
