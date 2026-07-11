package cmd

import (
	"fmt"
	"runtime"

	"strings"

	"github.com/kennedyowusu/koolbase-cli/internal/api"
	"github.com/kennedyowusu/koolbase-cli/internal/config"
	"github.com/kennedyowusu/koolbase-cli/internal/engine"
	"github.com/spf13/cobra"
)

var engineCmd = &cobra.Command{
	Use:   "engine",
	Short: "Manage Koolbase Flutter engines for Code Push",
	Long:  "Install and manage the custom Flutter engines that power Koolbase Code Push.",
}

// hostArch maps Go's runtime arch to Koolbase engine arch names.
func hostArch() string {
	switch runtime.GOARCH {
	case "arm64":
		return "arm64"
	case "amd64":
		return "x64"
	default:
		return runtime.GOARCH
	}
}

// hostPlatform maps Go's runtime OS to Koolbase HOST platform names (the OS the
// build runs on). Only macos is supported today; linux/windows come later.
func hostPlatform() string {
	switch runtime.GOOS {
	case "darwin":
		return "macos"
	default:
		return runtime.GOOS
	}
}

// targetPlatform/targetArch are what the built app runs on. targetPlatform is
// Android for now; targetArch is selected by the --target-arch flag on install
// and list, normalized to a canonical registry token (arm64 | arm).
func targetPlatform() string {
	if engineTargetPlatform == "ios" {
		return "ios"
	}
	return "android"
}
func targetArch() string {
	if a, ok := canonicalTargetArch(engineTargetArch); ok {
		return a
	}
	return "arm64"
}

// engineTargetArch holds the --target-arch flag value for install/list. Default
// arm64 keeps existing behavior; "arm" selects armeabi-v7a.
var engineTargetArch string

// engineTargetPlatform holds the --target-platform flag value for install/list.
// Empty or "android" → android (default, back-compat); "ios" → iOS engines.
var engineTargetPlatform string

// canonicalTargetArch normalizes any accepted --target-arch spelling to the
// canonical registry token (arm64 | arm) that publish writes and install/list
// query with, so the two ends can never drift. ok is false for unknown values.
func canonicalTargetArch(s string) (string, bool) {
	switch s {
	case "arm64", "arm64-v8a", "":
		return "arm64", true
	case "arm", "armeabi-v7a":
		return "arm", true
	default:
		return "", false
	}
}

var engineListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available Koolbase engines",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		client := api.NewClient(cfg.BaseURL, cfg.APIKey)

		hostP, hostA := hostPlatform(), hostArch()
		targetP, targetA := targetPlatform(), targetArch()
		resp, err := client.ListEngines(hostP, hostA, targetP, targetA)
		if err != nil {
			return err
		}

		if resp.Count == 0 {
			fmt.Printf("No engines available for host %s/%s, target %s/%s.\n", hostP, hostA, targetP, targetA)
			return nil
		}

		// Mark which ones are installed locally.
		fmt.Printf("Available engines for host %s/%s, target %s/%s:\n\n", hostP, hostA, targetP, targetA)
		for _, e := range resp.Engines {
			installed, _ := engine.IsInstalled(e.Version)
			marker := " "
			if installed {
				marker = "✓"
			}
			fmt.Printf("  %s %s  (flutter %s, %.0f MB)\n",
				marker, e.Version, e.FlutterVersion, float64(e.SizeBytes)/(1024*1024))
		}
		fmt.Println("\n✓ = installed locally")
		fmt.Println("Install with: koolbase engine install <version>")
		return nil
	},
}

var engineInstallCmd = &cobra.Command{
	Use:   "install [flutter-version]",
	Short: "Download and install a Koolbase engine",
	Long: `Download and install the Koolbase engine for a given Flutter version.

If no version is given, uses the Flutter version detected from the current
project (pubspec / flutter --version). Currently you must pass it explicitly,
e.g.:

  koolbase engine install 3.22.3`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return fmt.Errorf("please specify a Flutter version, e.g. koolbase engine install 3.22.3")
		}
		flutterVersion := args[0]
		// Accept both the bare Flutter version ("3.32.0") and the full
		// Koolbase display string ("3.32.0-koolbase.1"). The registry matches
		// on the bare version, so strip any "-koolbase.<rev>" suffix here.
		if i := strings.Index(flutterVersion, "-koolbase."); i >= 0 {
			flutterVersion = flutterVersion[:i]
		}

		cfg, err := config.Load()
		if err != nil {
			return err
		}
		client := api.NewClient(cfg.BaseURL, cfg.APIKey)

		hostP, hostA := hostPlatform(), hostArch()
		targetP, targetA := targetPlatform(), targetArch()

		fmt.Printf("Resolving engine for flutter %s (host %s/%s -> target %s/%s)...\n", flutterVersion, hostP, hostA, targetP, targetA)
		dl, err := client.GetEngineDownload(flutterVersion, hostP, hostA, targetP, targetA)
		if err != nil {
			return err
		}

		// The version string the API publishes (e.g. 3.22.3-koolbase.1) is what
		// we install under. Re-list to get it; the download response doesn't
		// carry it, so derive from the list.
		list, err := client.ListEngines(hostP, hostA, targetP, targetA)
		if err != nil {
			return err
		}
		version := ""
		for _, e := range list.Engines {
			if e.FlutterVersion == flutterVersion {
				version = e.Version
				break
			}
		}
		if version == "" {
			version = flutterVersion + "-koolbase.1" // fallback
		}

		androidCfg, _, _ := androidEngineConfig(targetA)
		installed, _ := engine.IsInstalledArch(version, androidCfg)

		if installed {
			fmt.Printf("Engine %s (%s) already installed.\n", version, targetA)
			return nil
		}

		fmt.Printf("Downloading %s (%.0f MB)...\n", version, float64(dl.SizeBytes)/(1024*1024))
		progress := func(downloaded, total int64) {
			mb := float64(downloaded) / (1024 * 1024)
			if total > 0 {
				pct := int(float64(downloaded) / float64(total) * 100)
				fmt.Printf("\r  %d%% (%.0f/%.0f MB)   ", pct, mb, float64(total)/(1024*1024))
			} else {
				fmt.Printf("\r  %.0f MB downloaded   ", mb)
			}
		}

		if err := engine.Install(version, androidCfg, dl.URL, dl.SHA256, dl.Signature, dl.SizeBytes, progress); err != nil {
			fmt.Println()
			return err
		}
		fmt.Printf("\r  100%%\n")

		dir, _ := engine.VersionDir(version)
		fmt.Printf("\n✓ Installed %s\n", version)
		fmt.Printf("  %s\n", dir)
		fmt.Printf("\nBuild with: koolbase build %s --release\n", targetPlatform())
		return nil
	},
}

var enginePathCmd = &cobra.Command{
	Use:   "path [version]",
	Short: "Print the install path of an engine (for scripting)",
	Long: `Print the absolute install path of an installed engine version.

Useful for wiring Flutter directly:

  flutter build macos --release \
    --local-engine=mac_release_arm64 \
    --local-engine-host=mac_release_arm64 \
    --local-engine-src-path="$(koolbase engine path 3.22.3-koolbase.1)/src"`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		version := args[0]
		installed, err := engine.IsInstalled(version)
		if err != nil {
			return err
		}
		if !installed {
			return fmt.Errorf("engine %s is not installed (run: koolbase engine install)", version)
		}
		dir, err := engine.VersionDir(version)
		if err != nil {
			return err
		}
		fmt.Println(dir)
		return nil
	},
}

var engineRemoveCmd = &cobra.Command{
	Use:   "remove [version]",
	Short: "Remove an installed engine",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		version := args[0]
		if err := engine.Remove(version); err != nil {
			return err
		}
		fmt.Printf("✓ Removed %s\n", version)
		return nil
	},
}

func init() {
	engineCmd.AddCommand(engineListCmd)
	engineCmd.AddCommand(engineInstallCmd)
	engineCmd.AddCommand(enginePathCmd)
	engineCmd.AddCommand(engineRemoveCmd)

	engineListCmd.Flags().StringVar(&engineTargetArch, "target-arch", "arm64", "Target ABI to list: arm64 (default) or arm (armeabi-v7a)")
	engineInstallCmd.Flags().StringVar(&engineTargetArch, "target-arch", "arm64", "Target ABI to install: arm64 (default) or arm (armeabi-v7a)")
	engineListCmd.Flags().StringVar(&engineTargetPlatform, "target-platform", "android", "Target platform to list: android (default) or ios")
	engineInstallCmd.Flags().StringVar(&engineTargetPlatform, "target-platform", "android", "Target platform to install: android (default) or ios")
}
