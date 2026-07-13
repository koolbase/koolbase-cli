package oauth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

// PKCE holds a generated code verifier and its derived challenge.
// The verifier is a high-entropy random string kept secret by the CLI;
// the challenge (its SHA-256, base64url-encoded) is sent to Google in the
// authorize request. At token-exchange time the CLI sends the verifier, and
// Google confirms it hashes to the challenge — proving the same client that
// started the flow is completing it. This is what makes a public client
// (no confidential secret) safe against authorization-code interception.
type PKCE struct {
	Verifier  string
	Challenge string
}

// GeneratePKCE creates a new verifier/challenge pair using the S256 method.
func GeneratePKCE() (*PKCE, error) {
	// 32 random bytes → 43-char base64url verifier (within RFC 7636's 43–128 range).
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("generate pkce verifier: %w", err)
	}
	verifier := base64.RawURLEncoding.EncodeToString(b)

	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	return &PKCE{Verifier: verifier, Challenge: challenge}, nil
}

// GenerateState returns a random opaque value used to bind the browser
// redirect back to this CLI invocation, defending against cross-site request
// forgery on the callback. The CLI rejects any callback whose state doesn't
// match the value it generated.
func GenerateState() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
