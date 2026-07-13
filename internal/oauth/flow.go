package oauth

import (
	"context"
	"fmt"
	"time"
)

// Config holds the Google Desktop-client credentials the flow needs. These are
// compiled into the CLI: the client ID is public, and a Desktop-client secret
// is explicitly non-confidential (Google treats it as such because it ships in
// distributed binaries). PKCE, not the secret, secures the flow.
type Config struct {
	ClientID     string
	ClientSecret string
}

// Result is the outcome of a successful browser login: a Google id_token the
// caller forwards to the Koolbase API to obtain a session.
type Result struct {
	IDToken string
}

// browserPrompt is called with the authorize URL so the caller can print it
// (in addition to the flow attempting to auto-open the browser). Injected so
// the CLI controls exactly how it's shown to the user.
type browserPrompt func(url string)

// Run executes the full loopback OAuth flow:
//
//	generate PKCE + state → bind loopback listener → open browser to Google →
//	receive callback → exchange code for id_token.
//
// It blocks until completion, timeout, or ctx cancellation.
func Run(ctx context.Context, cfg Config, prompt browserPrompt) (*Result, error) {
	pkce, err := GeneratePKCE()
	if err != nil {
		return nil, err
	}
	state, err := GenerateState()
	if err != nil {
		return nil, err
	}

	cb, err := NewCallbackServer(state)
	if err != nil {
		return nil, err
	}
	cb.Start()

	redirectURI := cb.RedirectURI()
	authURL := BuildAuthorizeURL(cfg.ClientID, redirectURI, state, pkce.Challenge)

	// Best-effort auto-open; always show the URL too (SSH/headless safety).
	_ = OpenBrowser(authURL)
	prompt(authURL)

	// Give the user a generous but bounded window to complete consent.
	code, err := cb.Wait(ctx, 3*time.Minute)
	if err != nil {
		return nil, err
	}

	idToken, err := ExchangeCode(ctx, cfg.ClientID, cfg.ClientSecret, code, pkce.Verifier, redirectURI)
	if err != nil {
		return nil, fmt.Errorf("exchange authorization code: %w", err)
	}

	return &Result{IDToken: idToken}, nil
}
