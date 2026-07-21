package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// serverReturning stands up a test server that always responds with the given
// status and raw JSON body, and returns a Client pointed at it.
func serverReturning(t *testing.T, status int, jsonBody string) (*Client, func()) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(jsonBody))
	}))
	return NewClient(srv.URL, ""), srv.Close
}

// A 409 with code "account_exists" must map to the actionable message that
// names `koolbase login --password` — the KB-6 fix. A password/email user who
// tries Google login should be steered to their real method, not shown raw
// server text.
func TestDo_ConflictAccountExists_NamesPasswordLogin(t *testing.T) {
	client, closeSrv := serverReturning(t, http.StatusConflict,
		`{"code":"account_exists","error":"an account with this email already exists — sign in with your existing method, then connect this provider from settings"}`)
	defer closeSrv()

	_, status, err := client.do("POST", "/v1/auth/login/google", nil)

	if status != http.StatusConflict {
		t.Fatalf("expected status 409, got %d", status)
	}
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "koolbase login --password") {
		t.Fatalf("expected message to name `koolbase login --password`, got: %q", err.Error())
	}
}

// Negative control: a 409 with a DIFFERENT code must NOT be hijacked by the
// account_exists message. Proves we match on the code, not merely on the 409
// status — otherwise every conflict (e.g. last_credential, email_in_use) would
// wrongly tell users to run --password.
func TestDo_ConflictOtherCode_NotHijacked(t *testing.T) {
	client, closeSrv := serverReturning(t, http.StatusConflict,
		`{"code":"email_in_use","error":"email already in use"}`)
	defer closeSrv()

	_, status, err := client.do("POST", "/v1/auth/register", nil)

	if status != http.StatusConflict {
		t.Fatalf("expected status 409, got %d", status)
	}
	if err != nil && strings.Contains(err.Error(), "koolbase login --password") {
		t.Fatalf("account_exists message wrongly applied to a different 409 code: %q", err.Error())
	}
}
