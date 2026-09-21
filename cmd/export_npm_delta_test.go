package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackageJSONDeltaReportsWhatIsMissing(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"),
		[]byte(`{"dependencies":{"expo":"~57.0.24"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	incoming := map[string][]byte{"package.json": []byte(
		`{"dependencies":{"expo":"~57.0.24","react-native-paper":"^5.15.3"},` +
			`"devDependencies":{"typescript":"~6.0.3"}}`)}
	var out bytes.Buffer
	reportPackageJSONDelta(incoming, dir, &out)
	got := out.String()
	for _, want := range []string{
		"  react-native-paper: ^5.15.3",
		"  typescript: ~6.0.3",
		"npx expo install react-native-paper typescript",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "  expo: ") {
		t.Errorf("reported a dependency the project already has:\n%s", got)
	}
}

func TestPackageJSONDeltaIsSilentForFlutterExports(t *testing.T) {
	var out bytes.Buffer
	reportPackageJSONDelta(map[string][]byte{"pubspec.yaml": []byte("name: x\n")}, t.TempDir(), &out)
	if out.Len() != 0 {
		t.Errorf("a Flutter export produced npm output: %q", out.String())
	}
}
