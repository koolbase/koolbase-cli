package cmd

import (
	"fmt"
	"runtime"

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

// hostPlatform maps Go's runtime OS to Koolbase platform names.
// Only macos is supported today; android/ios come later.
func hostPlatform() string {
	switch runtime.GOOS {
	case "darwin":
		return "macos"
	default:
		return runtime.GOOS
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

		platform := hostPlatform()
		arch := hostArch()
		resp, err := client.ListEngines(platform, arch)
		if err != nil {
			return err
		}

		if resp.Count == 0 {
			fmt.Printf("No engines available for %s/%s.\n", platform, arch)
			return nil
		}

		// Mark which ones are installed locally.
		fmt.Printf("Available engines for %s/%s:\n\n", platform, arch)
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

		cfg, err := config.Load()
		if err != nil {
			return err
		}
		client := api.NewClient(cfg.BaseURL, cfg.APIKey)

		platform := hostPlatform()
		arch := hostArch()

		fmt.Printf("Resolving engine for flutter %s (%s/%s)...\n", flutterVersion, platform, arch)
		dl, err := client.GetEngineDownload(flutterVersion, platform, arch)
		if err != nil {
			return err
		}

		// The version string the API publishes (e.g. 3.22.3-koolbase.1) is what
		// we install under. Re-list to get it; the download response doesn't
		// carry it, so derive from the list.
		list, err := client.ListEngines(platform, arch)
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

		installed, _ := engine.IsInstalled(version)
		if installed {
			fmt.Printf("Engine %s already installed.\n", version)
			return nil
		}

		fmt.Printf("Downloading %s (%.0f MB)...\n", version, float64(dl.SizeBytes)/(1024*1024))
		var lastPct int
		progress := func(downloaded, total int64) {
			if total <= 0 {
				return
			}
			pct := int(float64(downloaded) / float64(total) * 100)
			if pct != lastPct && pct%5 == 0 {
				fmt.Printf("\r  %d%%", pct)
				lastPct = pct
			}
		}

		if err := engine.Install(version, dl.URL, dl.SHA256, dl.SizeBytes, progress); err != nil {
			fmt.Println()
			return err
		}
		fmt.Printf("\r  100%%\n")

		dir, _ := engine.VersionDir(version)
		fmt.Printf("\n✓ Installed %s\n", version)
		fmt.Printf("  %s\n", dir)
		fmt.Println("\nBuild with: koolbase build macos --release")
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
}
