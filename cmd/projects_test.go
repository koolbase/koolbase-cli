package cmd

import (
	"strings"
	"testing"

	"github.com/kennedyowusu/koolbase-cli/internal/api"
)

func TestFormatProjectsMarksDefault(t *testing.T) {
	out := formatProjects([]api.Project{{ID: "p-1", Name: "Alpha"}, {ID: "p-2", Name: "Beta"}}, "p-2")
	for _, want := range []string{"p-1", "Alpha", "* p-2", "Beta", "your default project", "--project <id>"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "* p-1") {
		t.Errorf("non-default project marked:\n%s", out)
	}
}

func TestFormatProjectsNoDefault(t *testing.T) {
	out := formatProjects([]api.Project{{ID: "p-1", Name: "Alpha"}}, "")
	if strings.Contains(out, "your default project") || strings.Contains(out, "* p-1") {
		t.Errorf("unexpected default marker:\n%s", out)
	}
}

func TestFormatProjectsEmpty(t *testing.T) {
	if out := formatProjects(nil, ""); !strings.Contains(out, "No projects") {
		t.Errorf("unexpected output for no projects:\n%s", out)
	}
}
