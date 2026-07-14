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

const (
	githubAuthEndpoint  = "https://github.com/login/oauth/authorize"
	githubTokenEndpoint = "https://github.com/login/oauth/access_token"
)

// GitHubConfig holds the CLI's GitHub OAuth app credentials. Like the Google
// Desktop client, the secret is compiled in and treated as non-confidential
// for a native app; PKCE secures the exchange against code interception.
type GitHubConfig struct {
	ClientID     string
	ClientSecret string
}

// GitHubResult is the outcome of a successful GitHub browser login: an OAuth
// access token the API verifies by calling GitHub's API. Unlike Google, GitHub
// is OAuth2 (not OIDC) — there is no id_token, so the access token itself is
// what the API uses to resolve the verified identity server-side.
type GitHubResult struct {
	AccessToken string
}

// BuildGitHubAuthorizeURL constructs the GitHub consent URL. The read:user and
// user:email scopes let the API read the profile and the primary verified
// email. GitHub supports PKCE, so we include the S256 challenge.
func BuildGitHubAuthorizeURL(clientID, redirectURI, state, challenge string) string {
	q := url.Values{
		"client_id":             {clientID},
		"redirect_uri":          {redirectURI},
		"scope":                 {"read:user user:email"},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	return githubAuthEndpoint + "?" + q.Encode()
}

// githubTokenResponse is the subset of GitHub's token response we read. GitHub
// returns form-encoded by default; we request JSON via the Accept header.
type githubTokenResponse struct {
	AccessToken      string `json:"access_token"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// ExchangeGitHubCode trades an authorization code for a GitHub access token.
func ExchangeGitHubCode(ctx context.Context, clientID, clientSecret, code, verifier, redirectURI string) (string, error) {
	form := url.Values{
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"code":          {code},
		"code_verifier": {verifier},
		"grant_type":    {"authorization_code"},
		"redirect_uri":  {redirectURI},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, githubTokenEndpoint,
		strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("build github token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// GitHub returns form-encoded unless we ask for JSON.
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("github token exchange request: %w", err)
	}
	defer resp.Body.Close()

	var tr githubTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return "", fmt.Errorf("decode github token response: %w", err)
	}

	if tr.Error != "" {
		return "", fmt.Errorf("github token exchange failed: %s (%s)", tr.Error, tr.ErrorDescription)
	}
	if tr.AccessToken == "" {
		return "", fmt.Errorf("github returned no access token")
	}

	return tr.AccessToken, nil
}

// RunGitHub executes the full GitHub loopback OAuth flow, mirroring Run but
// returning a GitHub access token instead of a Google id_token.
func RunGitHub(ctx context.Context, cfg GitHubConfig, prompt browserPrompt) (*GitHubResult, error) {
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
	authURL := BuildGitHubAuthorizeURL(cfg.ClientID, redirectURI, state, pkce.Challenge)

	_ = OpenBrowser(authURL)
	prompt(authURL)

	code, err := cb.Wait(ctx, 3*time.Minute)
	if err != nil {
		return nil, err
	}

	accessToken, err := ExchangeGitHubCode(ctx, cfg.ClientID, cfg.ClientSecret, code, pkce.Verifier, redirectURI)
	if err != nil {
		return nil, fmt.Errorf("exchange github authorization code: %w", err)
	}

	return &GitHubResult{AccessToken: accessToken}, nil
}
