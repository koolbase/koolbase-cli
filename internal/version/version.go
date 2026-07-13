// Package version holds the CLI's version, stamped at build time via:
//
//	go build -ldflags "-X github.com/kennedyowusu/koolbase-cli/internal/version.Version=v0.4.0"
//
// Unstamped builds report "dev".
package version

var Version = "dev"
