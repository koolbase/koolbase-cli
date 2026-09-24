package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fakeServer(t *testing.T, status int, body string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return NewClient(srv.URL, "")
}

func TestLoginShowsTheServersReason(t *testing.T) {
	c := fakeServer(t, http.StatusForbidden, `{"error":"please verify your email before logging in"}`)
	_, err := c.Login("someone@example.test", "a-long-password")
	if err == nil || !strings.Contains(err.Error(), "please verify your email") || strings.Contains(err.Error(), "lacks access") {
		t.Errorf("unverified login error = %v; want the server's verify-your-email reason", err)
	}
	c = fakeServer(t, http.StatusUnauthorized, `{"error":"invalid email or password"}`)
	_, err = c.Login("someone@example.test", "wrong-password")
	if err == nil || !strings.Contains(err.Error(), "invalid email or password") || strings.Contains(err.Error(), "authentication as") {
		t.Errorf("wrong-password login error = %v; want the server's credential message", err)
	}
}

func TestLoginKeepsTheOAuthOnlyGuidance(t *testing.T) {
	c := fakeServer(t, http.StatusForbidden, `{"code":"oauth_only_account","error":"no password"}`)
	_, err := c.Login("someone@example.test", "a-long-password")
	if err == nil || !strings.Contains(err.Error(), "Google or Apple sign-in") {
		t.Errorf("oauth-only login error = %v; want the existing Google/Apple guidance", err)
	}
}

func TestProjectCallsKeepTheAccountGuidance(t *testing.T) {
	c := fakeServer(t, http.StatusForbidden, `{"error":"forbidden"}`)
	_, _, err := c.do("GET", "/v1/projects/p1/functions", nil)
	if err == nil || !strings.Contains(err.Error(), "lacks access to this resource") {
		t.Errorf("project 403 error = %v; want the existing account-access guidance", err)
	}
}
