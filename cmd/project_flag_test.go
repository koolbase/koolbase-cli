package cmd

import (
	"testing"

	"github.com/spf13/cobra"
)

// Every command that takes --project also accepts -p: users learn one form
// from deploy or invoke and expect it everywhere (seed apply once refused it).
// Walking the tree also builds every command's flags, so a clashing shorthand
// would fail here rather than when a user runs the CLI.
func TestProjectFlagHasShorthand(t *testing.T) {
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if f := c.LocalFlags().Lookup("project"); f != nil && f.Shorthand != "p" {
			t.Errorf("%s: --project has no -p shorthand", c.CommandPath())
		}
		if f := c.PersistentFlags().Lookup("project"); f != nil && f.Shorthand != "p" {
			t.Errorf("%s: persistent --project has no -p shorthand", c.CommandPath())
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(rootCmd)
}
