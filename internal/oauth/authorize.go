package oauth

import (
	"net/url"
	"os/exec"
	"runtime"
)

// googleAuthEndpoint is Google's OAuth 2.0 authorization (consent) URL.
const googleAuthEndpoint = "https://accounts.google.com/o/oauth2/v2/auth"

// BuildAuthorizeURL constructs the Google consent URL the user's browser is
// sent to. scope openid+email+profile yields an id_token carrying the verified
// email and stable subject the API needs. The S256 challenge binds this
// request to the PKCE verifier held by the CLI.
func BuildAuthorizeURL(clientID, redirectURI, state, challenge string) string {
	q := url.Values{
		"client_id":             {clientID},
		"redirect_uri":          {redirectURI},
		"response_type":         {"code"},
		"scope":                 {"openid email profile"},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		// Ask Google to always show the account chooser rather than silently
		// reusing a single logged-in account — the user may have several.
		"prompt": {"select_account"},
	}
	return googleAuthEndpoint + "?" + q.Encode()
}

// OpenBrowser attempts to open url in the user's default browser. Failure is
// not fatal — the caller should always also print the URL so the user can open
// it manually (headless machines, SSH sessions, locked-down environments).
func OpenBrowser(url string) error {
	var cmd string
	var args []string

	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
		args = []string{url}
	case "windows":
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", url}
	default: // linux, bsd, etc.
		cmd = "xdg-open"
		args = []string{url}
	}

	return exec.Command(cmd, args...).Start()
}
