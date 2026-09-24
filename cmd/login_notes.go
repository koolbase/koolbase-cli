package cmd

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kennedyowusu/koolbase-cli/internal/config"
)

// A sign-in method (Google, GitHub) can be connected to a Koolbase account with
// a different email, so choosing one account can land in another. These notes
// say so at login instead of leaving it to be discovered later.

// googleTokenEmail reads the email from a Google ID token for display only.
// The server verifies the token independently; nothing here trusts it.
func googleTokenEmail(idToken string) string {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return ""
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return ""
	}
	var claims struct {
		Email string `json:"email"`
	}
	if json.Unmarshal(raw, &claims) != nil {
		return ""
	}
	return claims.Email
}

// previousSessionEmail is the email of the saved session, or "" if none.
// Only the email is used; the saved key is never read out.
func previousSessionEmail() string {
	cfg, err := config.Load()
	if err != nil || cfg == nil {
		return ""
	}
	return cfg.Email
}

// loginNotes explains a mismatch between the account chosen at the provider
// and the Koolbase account signed in to, and a switch from a previous account.
func loginNotes(provider, chosenEmail, accountEmail, previousEmail string) string {
	var b strings.Builder
	if provider != "" && chosenEmail != "" && !strings.EqualFold(chosenEmail, accountEmail) {
		fmt.Fprintf(&b, "\n Note: you chose the %s account %s, which is connected to the Koolbase account %s.\n", provider, chosenEmail, accountEmail)
		fmt.Fprintf(&b, "       To change that, open Settings > Connected in the dashboard while signed in as %s.\n", accountEmail)
	}
	if previousEmail != "" && !strings.EqualFold(previousEmail, accountEmail) {
		fmt.Fprintf(&b, "\n Switched account: you were signed in as %s.\n", previousEmail)
	}
	return b.String()
}
