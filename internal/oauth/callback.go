package oauth

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"
)

// callbackResult carries the outcome of the single OAuth redirect back to the
// waiting CLI goroutine — either an authorization code or an error.
type callbackResult struct {
	code string
	err  error
}

// CallbackServer is a one-shot local HTTP server that listens on an ephemeral
// loopback port for Google's OAuth redirect. Desktop-type OAuth clients permit
// any 127.0.0.1:<port> redirect URI, so we let the OS choose a free port and
// build the redirect URI from it.
type CallbackServer struct {
	listener net.Listener
	server   *http.Server
	results  chan callbackResult
	state    string
}

// NewCallbackServer binds a loopback listener on an OS-chosen port. The caller
// must later call RedirectURI() to learn the address to register with Google,
// and Wait() to block for the redirect. state is the CSRF value that the
// incoming callback must echo back.
func NewCallbackServer(state string) (*CallbackServer, error) {
	// ":0" on the loopback interface → kernel assigns a free ephemeral port.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("bind loopback listener: %w", err)
	}
	cs := &CallbackServer{
		listener: l,
		results:  make(chan callbackResult, 1),
		state:    state,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", cs.handleCallback)
	cs.server = &http.Server{Handler: mux}
	return cs, nil
}

// RedirectURI returns the exact redirect_uri to send to Google, including the
// chosen port. Must match what the authorize request declares.
func (cs *CallbackServer) RedirectURI() string {
	return fmt.Sprintf("http://127.0.0.1:%d/callback", cs.listener.Addr().(*net.TCPAddr).Port)
}

// Start begins serving in the background. It returns immediately.
func (cs *CallbackServer) Start() {
	go func() {
		// Serve returns ErrServerClosed on graceful shutdown; that's expected.
		_ = cs.server.Serve(cs.listener)
	}()
}

// handleCallback processes the single redirect. It validates state, extracts
// either the code or an OAuth error, renders a human-facing page, and pushes
// the result to the waiting goroutine.
func (cs *CallbackServer) handleCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	// An OAuth error (user denied consent, etc.) comes back as ?error=...
	if oauthErr := q.Get("error"); oauthErr != "" {
		cs.finish(w, false, fmt.Errorf("authorization denied: %s", oauthErr))
		return
	}

	// CSRF defense: the returned state must match what we generated.
	if q.Get("state") != cs.state {
		cs.finish(w, false, fmt.Errorf("state mismatch — possible CSRF, aborting"))
		return
	}

	code := q.Get("code")
	if code == "" {
		cs.finish(w, false, fmt.Errorf("no authorization code in callback"))
		return
	}

	cs.finish(w, true, nil)
	cs.results <- callbackResult{code: code}
}

// finish renders the browser-facing page and, on failure, also signals the
// waiting goroutine. On success the caller sends the code after this returns.
func (cs *CallbackServer) finish(w http.ResponseWriter, ok bool, err error) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if ok {
		fmt.Fprint(w, successPage)
		return
	}
	w.WriteHeader(http.StatusBadRequest)
	fmt.Fprintf(w, failurePage, err)
	cs.results <- callbackResult{err: err}
}

// Wait blocks until the callback arrives, the timeout elapses, or ctx is
// cancelled, then shuts the server down. Returns the authorization code.
func (cs *CallbackServer) Wait(ctx context.Context, timeout time.Duration) (string, error) {
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = cs.server.Shutdown(shutdownCtx)
	}()

	select {
	case res := <-cs.results:
		return res.code, res.err
	case <-time.After(timeout):
		return "", fmt.Errorf("timed out waiting for browser authorization after %s", timeout)
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

const successPage = `<!doctype html><html><head><meta charset="utf-8"><title>Koolbase</title></head>
<body style="font-family:system-ui;text-align:center;padding-top:80px">
<h2>You're signed in to the Koolbase CLI</h2>
<p>You can close this tab and return to your terminal.</p>
</body></html>`

const failurePage = `<!doctype html><html><head><meta charset="utf-8"><title>Koolbase</title></head>
<body style="font-family:system-ui;text-align:center;padding-top:80px">
<h2>Sign-in failed</h2>
<p>%v</p>
<p>Return to your terminal and try again.</p>
</body></html>`
