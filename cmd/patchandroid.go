package cmd

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/kennedyowusu/koolbase-cli/internal/config"
	"github.com/spf13/cobra"
)

var (
	patchAndroidBaseAAB     string
	patchAndroidEngine      string
	patchAndroidFlutterSDK  string
	patchAndroidArchs       []string
	patchAndroidProject     string
	patchAndroidChannel     string
	patchAndroidKey         string
	patchAndroidDiff        bool
	patchAndroidPublish     bool
	patchAndroidRollout     int
	patchAndroidMandatory   bool
	patchAndroidNotes       string
	patchAndroidNoTreeShake bool
)

var patchAndroidCmd = &cobra.Command{
	Use:   "android",
	Short: "Build new ABIs and push Code Push patches against a released AAB",
	Long: `Push a Code Push patch for every target ABI in one step.

For each ABI it pulls the base libapp.so out of your released AAB (--base-aab),
rebuilds the current source with the Koolbase engine, and pushes a patch that
diffs base -> new to the matching build_id release. Multi-ABI companion to
'koolbase patch push' (which handles one binary at a time).

Examples:
  koolbase patch android --base-aab build/app/outputs/koolbase/app-release.aab --engine 3.44.0-koolbase.2 --flutter-sdk ~/flutter-3.44.0 --publish
  koolbase patch android --base-aab app-release.aab --target-archs arm64,arm --key ~/keys/private.key`,
	Args: cobra.NoArgs,
	RunE: runPatchAndroid,
}

func runPatchAndroid(cmd *cobra.Command, args []string) error {
	if patchAndroidBaseAAB == "" {
		return fmt.Errorf("--base-aab is required (the released AAB whose libapp.so we diff against)")
	}
	if _, err := os.Stat(patchAndroidBaseAAB); err != nil {
		return fmt.Errorf("--base-aab not found: %s", patchAndroidBaseAAB)
	}
	if len(patchAndroidArchs) == 0 {
		return fmt.Errorf("no target ABIs — pass --target-archs (e.g. arm64,arm)")
	}

	version := patchAndroidEngine
	if version == "" {
		resolved, err := resolveProjectEngine()
		if err != nil {
			return err
		}
		version = resolved
	}

	buildFlutterSDK = patchAndroidFlutterSDK
	flutterBin, sdkSource, err := resolveFlutterBin(version)
	if err != nil {
		return err
	}
	if verr := verifyFlutterVersion(flutterBin, baseFlutterVersion(version)); verr != nil {
		return verr
	}

	projectID := patchAndroidProject
	if projectID == "" {
		if cfg, cerr := config.Load(); cerr == nil {
			projectID = cfg.ProjectID
		}
	}
	if projectID == "" {
		return fmt.Errorf("no project ID — pass --app or save one in your config")
	}

	projectDir, _ := os.Getwd()
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate koolbase binary: %w", err)
	}

	work, err := os.MkdirTemp("", "koolbase-patch-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)

	// Stage the base AAB out of build/ up front: each per-ABI build runs
	// `flutter clean`, which wipes build/ — a --base-aab under build/ would
	// otherwise vanish before the second ABI reads it.
	safeBaseAAB := filepath.Join(work, "base.aab")
	if cerr := copyPlainFile(patchAndroidBaseAAB, safeBaseAAB); cerr != nil {
		return fmt.Errorf("stage base AAB: %w", cerr)
	}

	fmt.Printf("Koolbase patch android: engine %s, ABIs %s\n", version, strings.Join(patchAndroidArchs, ", "))
	fmt.Printf("  Flutter SDK: %s (%s)\n", flutterBin, sdkSource)
	fmt.Printf("  Base AAB:    %s\n", patchAndroidBaseAAB)
	fmt.Printf("  Project: %s   channel: %s   diff: %v   publish: %v\n\n",
		projectID, patchAndroidChannel, patchAndroidDiff, patchAndroidPublish)

	var patched, skipped []string
	for _, arch := range patchAndroidArchs {
		if arch != "arm64" && arch != "arm" {
			fmt.Printf("\u26a0 skipping %s — Koolbase has no engine for it yet\n\n", arch)
			skipped = append(skipped, arch)
			continue
		}
		abiDir := androidABIDir(arch)
		fmt.Printf("=== patching ABI: %s (%s) ===\n", arch, abiDir)

		abiWork := filepath.Join(work, abiDir)
		if err := os.MkdirAll(abiWork, 0o755); err != nil {
			return err
		}

		baseLibapp := filepath.Join(abiWork, "base_libapp.so")
		if err := extractLibappFromAAB(safeBaseAAB, abiDir, baseLibapp); err != nil {
			return fmt.Errorf("extract base libapp for %s: %w", arch, err)
		}

		clean := exec.Command(flutterBin, "clean")
		clean.Stdout, clean.Stderr = os.Stdout, os.Stderr
		_ = clean.Run()

		buildArgs := []string{"build", "android", "--engine", version, "--target-arch", arch, "--release"}
		if patchAndroidFlutterSDK != "" {
			buildArgs = append(buildArgs, "--flutter-sdk", patchAndroidFlutterSDK)
		}
		if patchAndroidNoTreeShake {
			buildArgs = append(buildArgs, "--no-tree-shake-icons")
		}
		b := exec.Command(self, buildArgs...)
		b.Stdout, b.Stderr, b.Stdin = os.Stdout, os.Stderr, os.Stdin
		if err := b.Run(); err != nil {
			return fmt.Errorf("build ABI %s failed: %w", arch, err)
		}

		apk := filepath.Join(projectDir, "build", "app", "outputs", "flutter-apk", "app-release.apk")
		if err := extractLibsFromAPK(apk, abiDir, abiWork); err != nil {
			return fmt.Errorf("extract new libs for %s: %w", arch, err)
		}
		newLibapp := filepath.Join(abiWork, "libapp.so")

		pushArgs := []string{"patch", "push",
			"--app", projectID,
			"--binary", baseLibapp,
			"--new", newLibapp,
			"--platform", "android",
			"--channel", patchAndroidChannel,
			"--key", patchAndroidKey,
			"--rollout", fmt.Sprintf("%d", patchAndroidRollout),
		}
		if patchAndroidDiff {
			pushArgs = append(pushArgs, "--diff")
		}
		if patchAndroidPublish {
			pushArgs = append(pushArgs, "--publish")
		}
		if patchAndroidMandatory {
			pushArgs = append(pushArgs, "--mandatory")
		}
		if patchAndroidNotes != "" {
			pushArgs = append(pushArgs, "--notes", patchAndroidNotes)
		}
		p := exec.Command(self, pushArgs...)
		p.Stdout, p.Stderr, p.Stdin = os.Stdout, os.Stderr, os.Stdin
		if err := p.Run(); err != nil {
			return fmt.Errorf("patch push for %s failed: %w", arch, err)
		}
		fmt.Printf("  \u2713 %s patched\n\n", abiDir)
		patched = append(patched, abiDir)
	}

	fmt.Println("\u2713 patch android complete")
	for _, a := range patched {
		fmt.Printf("    %-13s patched on %s\n", a, patchAndroidChannel)
	}
	if len(skipped) > 0 {
		fmt.Printf("    skipped (no engine): %s\n", strings.Join(skipped, ", "))
	}
	if !patchAndroidPublish && len(patched) > 0 {
		fmt.Println("\n  Patches uploaded as DRAFTS — re-run with --publish, or publish each with 'koolbase patch publish'.")
	}
	return nil
}

// copyPlainFile copies src to dst byte-for-byte.
func copyPlainFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// extractLibappFromAAB pulls base/lib/<abiDir>/libapp.so out of a Koolbase
// release AAB into dest.
func extractLibappFromAAB(aabPath, abiDir, dest string) error {
	zr, err := zip.OpenReader(aabPath)
	if err != nil {
		return err
	}
	defer zr.Close()
	want := "base/lib/" + abiDir + "/libapp.so"
	for _, f := range zr.File {
		if f.Name == want {
			return copyZipEntryToFile(f, dest)
		}
	}
	return fmt.Errorf("%s not found in %s (is this a Koolbase release AAB?)", want, filepath.Base(aabPath))
}

func init() {
	patchAndroidCmd.Flags().StringVar(&patchAndroidBaseAAB, "base-aab", "", "Path to the released Koolbase AAB to diff against (required)")
	patchAndroidCmd.Flags().StringVar(&patchAndroidEngine, "engine", "", "Engine version (e.g. 3.44.0-koolbase.2)")
	patchAndroidCmd.Flags().StringVar(&patchAndroidFlutterSDK, "flutter-sdk", "", "Path to a version-matched Flutter SDK")
	patchAndroidCmd.Flags().StringSliceVar(&patchAndroidArchs, "target-archs", []string{"arm64", "arm"}, "Target ABIs (comma-separated): arm64,arm")
	patchAndroidCmd.Flags().StringVar(&patchAndroidProject, "app", "", "Koolbase project/app ID (defaults to saved config)")
	patchAndroidCmd.Flags().StringVar(&patchAndroidChannel, "channel", "stable", "Release channel")
	patchAndroidCmd.Flags().StringVar(&patchAndroidKey, "key", "private.key", "Path to Ed25519 patch signing key")
	patchAndroidCmd.Flags().BoolVar(&patchAndroidDiff, "diff", true, "Build kind=4 DIFF patches (smaller); --diff=false for full kind=3")
	patchAndroidCmd.Flags().BoolVar(&patchAndroidPublish, "publish", false, "Publish each patch immediately after upload")
	patchAndroidCmd.Flags().IntVar(&patchAndroidRollout, "rollout", 100, "Rollout percentage 0-100")
	patchAndroidCmd.Flags().BoolVar(&patchAndroidMandatory, "mandatory", false, "Mark patches mandatory (force-update)")
	patchAndroidCmd.Flags().StringVar(&patchAndroidNotes, "notes", "", "Release notes")
	patchAndroidCmd.Flags().BoolVar(&patchAndroidNoTreeShake, "no-tree-shake-icons", false, "Disable icon tree-shaking")
	patchCmd.AddCommand(patchAndroidCmd)
}
