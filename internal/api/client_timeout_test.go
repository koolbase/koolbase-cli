package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The control-plane client must fail fast; the invoke client must outlast it.
// Server sleeps 2s; a 1s-timeout control client gives up, the long client
// (bounded by invokeTimeout) succeeds. Proves InvokeFunction's requests are
// governed by longClient rather than the 30s default — the bug where a
// 30000ms-execution function could never be invoked through the CLI because
// the request died at exactly 30s.
func TestInvokeUsesLongClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status_code":200}`))
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "test")
	client.httpClient.Timeout = 1 * time.Second // tighten to make the race observable in test time

	// Control-plane path: must time out.
	if _, _, err := client.do("GET", "/v1/whoami", nil); err == nil {
		t.Fatal("expected control-plane request to time out, got nil error")
	}

	// Invoke path: same server, same slowness — must succeed.
	if _, err := client.InvokeFunction("p1", "slow_fn", nil); err != nil {
		t.Fatalf("expected invoke to outlast the delay via longClient, got: %v", err)
	}
}
