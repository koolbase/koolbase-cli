package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// googleTokenEndpoint is Google's OAuth 2.0 token exchange URL.
const googleTokenEndpoint = "https://oauth2.googleapis.com/token"

// tokenResponse is the subset of Google's token response we need. We only care
// about id_token — a signed JWT the API verifies server-side against Google's
// JWKS. We deliberately ignore access_token/refresh_token: the CLI never calls
// Google APIs on the user's behalf, it only needs proof of identity.
type tokenResponse struct {
	IDToken          string `json:"id_token"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// ExchangeCode trades an authorization code for a Google id_token using the
// PKCE verifier. For a Desktop-type client the "secret" is non-confidential
// (it ships in the binary); PKCE is what actually secures the exchange.
func ExchangeCode(ctx context.Context, clientID, clientSecret, code, verifier, redirectURI string) (string, error) {
	form := url.Values{
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"code":          {code},
		"code_verifier": {verifier},
		"grant_type":    {"authorization_code"},
		"redirect_uri":  {redirectURI},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, googleTokenEndpoint,
		strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("token exchange request: %w", err)
	}
	defer resp.Body.Close()

	var tr tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}

	if tr.Error != "" {
		return "", fmt.Errorf("google token exchange failed: %s (%s)", tr.Error, tr.ErrorDescription)
	}
	if tr.IDToken == "" {
		return "", fmt.Errorf("google returned no id_token")
	}

	return tr.IDToken, nil
}
