package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"github.com/creativeprojects/go-selfupdate"
)

const (
	repoOwner = "okayest-dev"
	repoName  = "genie"
)

type selfUpdateConfig struct {
	pre       bool
	auto      bool
	checkOnly bool
}

func runSelfUpdateCmd(args []string, stdout, stderr io.Writer) int {
	cfg, err := parseSelfUpdateArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		printSelfUpdateHelp(stderr)
		return 3
	}

	if err := runSelfUpdate(cfg, stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	return 0
}

func parseSelfUpdateArgs(args []string) (selfUpdateConfig, error) {
	cfg := selfUpdateConfig{}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--pre":
			cfg.pre = true
		case "--auto":
			cfg.auto = true
		case "--check-only":
			cfg.checkOnly = true
		case "-h", "--help":
			printSelfUpdateHelp(os.Stdout)
			os.Exit(0)
		default:
			if strings.HasPrefix(arg, "-") {
				return cfg, fmt.Errorf("unknown flag: %s", arg)
			}
			return cfg, fmt.Errorf("unexpected argument: %s", arg)
		}
	}

	return cfg, nil
}

func printSelfUpdateHelp(w io.Writer) {
	fmt.Fprintln(w, `usage: genie self-update [options]

Options:
  --pre         Include pre-releases when checking for updates
  --auto        Skip confirmation prompt
  --check-only  Only check for updates, don't apply
  -h, --help    Show this help message`)
}

func runSelfUpdate(cfg selfUpdateConfig, stdout, stderr io.Writer) error {
	currentVersion := getCurrentVersion()
	fmt.Fprintf(stdout, "Current version: %s\n", currentVersion)

	// Detect package manager
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	pm := detectPackageManager(exePath)
	if pm != pkgUnknown {
		upgradeCmd := getPackageManagerUpgradeCmd(pm)
		fmt.Fprintf(stdout, "Detected package manager: %s\n", pm)
		fmt.Fprintf(stdout, "Please use your package manager to update:\n  %s\n", upgradeCmd)
		return nil
	}

	fmt.Fprintf(stdout, "No package manager detected, using self-update...\n")

	// Configure GitHub source
	githubSource, err := selfupdate.NewGitHubSource(selfupdate.GitHubConfig{
		// No API token needed for public repos
	})
	if err != nil {
		return fmt.Errorf("failed to create GitHub source: %w", err)
	}

	// Configure updater
	updater, err := selfupdate.NewUpdater(selfupdate.Config{
		Source:      githubSource,
		OS:          runtime.GOOS,
		Arch:        runtime.GOARCH,
		Prerelease:  cfg.pre,
		Filters:     []string{fmt.Sprintf("genie_.*_%s_%s\\.tar\\.gz", runtime.GOOS, runtime.GOARCH)},
	})
	if err != nil {
		return fmt.Errorf("failed to create updater: %w", err)
	}

	// Repository slug
	repo := selfupdate.NewRepositorySlug(repoOwner, repoName)

	// Check for updates
	release, found, err := updater.DetectLatest(context.Background(), repo)
	if err != nil {
		return fmt.Errorf("failed to check for updates: %w", err)
	}

	if !found {
		fmt.Fprintf(stdout, "Already up to date (%s)\n", currentVersion)
		return nil
	}

	fmt.Fprintf(stdout, "Update available: %s -> %s\n", currentVersion, release.Version())

	if cfg.checkOnly {
		return nil
	}

	// Prompt for confirmation unless --auto
	if !cfg.auto && isTerminal() {
		confirmed, err := promptConfirm(fmt.Sprintf("Update to %s?", release.Version()))
		if err != nil || !confirmed {
			fmt.Fprintln(stdout, "Update cancelled")
			return nil
		}
	}

	// Perform update
	release, err = updater.UpdateCommand(context.Background(), exePath, currentVersion, repo)
	if err != nil {
		if strings.Contains(err.Error(), "no update available") {
			fmt.Fprintf(stdout, "Already up to date (%s)\n", currentVersion)
			return nil
		}
		return fmt.Errorf("update failed: %w", err)
	}

	fmt.Fprintf(stdout, "Successfully updated to version %s\n", release.Version())
	return nil
}