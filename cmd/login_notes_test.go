package cmd

import (
	"encoding/base64"
	"strings"
	"testing"
)

func fakeIDToken(payload string) string {
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"RS256"}`)) + "." + enc([]byte(payload)) + ".sig"
}

func TestGoogleTokenEmail(t *testing.T) {
	if got := googleTokenEmail(fakeIDToken(`{"email":"a@gmail.com","sub":"1"}`)); got != "a@gmail.com" {
		t.Errorf("googleTokenEmail = %q, want a@gmail.com", got)
	}
	for _, bad := range []string{"", "not-a-token", "a.b", "a.%%%.c"} {
		if got := googleTokenEmail(bad); got != "" {
			t.Errorf("googleTokenEmail(%q) = %q, want empty", bad, got)
		}
	}
}

func TestLoginNotesMismatch(t *testing.T) {
	out := loginNotes("Google", "a@gmail.com", "k@work.com", "")
	if !strings.Contains(out, "you chose the Google account a@gmail.com, which is connected to the Koolbase account k@work.com") {
		t.Errorf("missing mismatch note:\n%s", out)
	}
	if strings.Contains(out, "Switched account") {
		t.Errorf("unexpected switch note:\n%s", out)
	}
}

func TestLoginNotesSwitch(t *testing.T) {
	out := loginNotes("GitHub", "", "a@gmail.com", "k@work.com")
	if !strings.Contains(out, "Switched account: you were signed in as k@work.com") || strings.Contains(out, "you chose") {
		t.Errorf("unexpected notes:\n%s", out)
	}
}

func TestLoginNotesQuietWhenAllMatch(t *testing.T) {
	if out := loginNotes("Google", "A@gmail.com", "a@gmail.com", "a@gmail.com"); out != "" {
		t.Errorf("expected no notes, got:\n%s", out)
	}
}
