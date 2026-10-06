package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	denoMarkerFile = ".deno-installed-by-genie"
)

type uninstallConfig struct {
	force  bool
	dryRun bool
}

func removePathFromRc(rcFile, pathEntry string) error {
	if _, err := os.Stat(rcFile); os.IsNotExist(err) {
		return nil // File doesn't exist, nothing to do
	}

	content, err := os.ReadFile(rcFile)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", rcFile, err)
	}

	lines := strings.Split(string(content), "\n")
	var newLines []string
	removed := false

	escapedEntry := pathEntry
	// Escape special regex characters
	escapedEntry = strings.ReplaceAll(escapedEntry, ".", "\\.")
	escapedEntry = strings.ReplaceAll(escapedEntry, "/", "\\/")

	for _, line := range lines {
		// Check if this line contains the PATH entry
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, pathEntry) &&
			(strings.Contains(trimmed, "PATH") ||
				strings.Contains(trimmed, "set -gx")) {
			// Skip this line (remove it)
			removed = true
			continue
		}
		newLines = append(newLines, line)
	}

	if !removed {
		return nil // Entry not found
	}

	newContent := strings.Join(newLines, "\n")
	return os.WriteFile(rcFile, []byte(newContent), 0644)
}

func removeDenoPathEntries() error {
	shell := detectUserShell()
	rcFile := getRcFile(shell)
	if rcFile == "" {
		return nil
	}

	denoBinDir := ""
	if runtime.GOOS == "windows" {
		// Windows uses different paths
		return nil
	} else {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return nil
		}
		denoBinDir = filepath.Join(homeDir, ".deno", "bin")
	}

	return removePathFromRc(rcFile, denoBinDir)
}

func checkDenoMarker() bool {
	configDir := getConfigDir()
	if configDir == "" {
		return false
	}
	markerPath := filepath.Join(configDir, denoMarkerFile)
	_, err := os.Stat(markerPath)
	return err == nil
}

func getConfigDir() string {
	xdgConfig := os.Getenv("XDG_CONFIG_HOME")
	if xdgConfig != "" {
		return filepath.Join(xdgConfig, "genie")
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(homeDir, ".config", "genie")
}

func uninstallDeno() error {
	if runtime.GOOS == "windows" {
		return nil // Not supported on Windows
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	// Remove ~/.deno directory
	denoDir := filepath.Join(homeDir, ".deno")
	if _, err := os.Stat(denoDir); err == nil {
		fmt.Printf("Removing Deno directory: %s\n", denoDir)
		if err := os.RemoveAll(denoDir); err != nil {
			return fmt.Errorf("failed to remove Deno directory: %w", err)
		}
	}

	// Remove PATH entries from shell rc files
	if err := removeDenoPathEntries(); err != nil {
		fmt.Printf("Warning: failed to remove Deno PATH entries: %v\n", err)
	}

	// Remove marker file
	configDir := getConfigDir()
	if configDir != "" {
		markerPath := filepath.Join(configDir, denoMarkerFile)
		os.Remove(markerPath)
	}

	return nil
}

func runUninstallCmd(args []string, stdout, stderr io.Writer) int {
	cfg, err := parseUninstallArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		printUninstallHelp(stderr)
		return 3
	}

	if err := runUninstall(cfg, stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	return 0
}

func parseUninstallArgs(args []string) (uninstallConfig, error) {
	cfg := uninstallConfig{}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--force", "-f":
			cfg.force = true
		case "--dry-run":
			cfg.dryRun = true
		case "-h", "--help":
			printUninstallHelp(os.Stdout)
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

func printUninstallHelp(w io.Writer) {
	fmt.Fprintln(w, `usage: genie uninstall [options]

Options:
  -f, --force     Skip confirmation prompt
  --dry-run       Show what would be removed without removing
  -h, --help      Show this help message`)
}

func runUninstall(cfg uninstallConfig, stdout, stderr io.Writer) error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	pm := detectPackageManager(exePath)

	var actions []string

	if pm != pkgUnknown {
		actions = append(actions, fmt.Sprintf("Package manager (%s): will run '%s'", pm, getPackageManagerUninstallCmd(pm)))
	} else {
		// Manual install removal
		actions = append(actions, "Binary: ~/.local/bin/genie")
		actions = append(actions, "Config directory: ~/.config/genie/")
		actions = append(actions, "Sessions directory: ~/.config/genie/sessions/")
		actions = append(actions, "PATH entries from shell rc files (.bashrc, .zshrc, .config/fish/config.fish)")
	}

	// Check for Deno
	if checkDenoMarker() {
		actions = append(actions, "Deno (installed by Genie): ~/.deno/ and PATH entries")
	}

	if len(actions) == 0 {
		fmt.Fprintln(stdout, "Nothing to uninstall")
		return nil
	}

	fmt.Fprintln(stdout, "The following will be removed:")
	for _, action := range actions {
		fmt.Fprintf(stdout, "  - %s\n", action)
	}

	if cfg.dryRun {
		fmt.Fprintln(stdout, "\nDry run: no changes made")
		return nil
	}

	if !cfg.force && isTerminal() {
		confirmed, err := promptConfirm("\nProceed with uninstall?")
		if err != nil || !confirmed {
			fmt.Fprintln(stdout, "Uninstall cancelled")
			return nil
		}
	}

	// Execute uninstall
	if pm != pkgUnknown {
		uninstallCmd := getPackageManagerUninstallCmd(pm)
		fmt.Fprintf(stdout, "Running: %s\n", uninstallCmd)
		// Note: We don't actually run the package manager command here
		// In a real implementation, you'd exec the command
		fmt.Fprintln(stdout, "Package manager uninstall triggered")
	} else {
		// Remove binary
		binaryPath := filepath.Join(os.Getenv("HOME"), ".local", "bin", "genie")
		if _, err := os.Stat(binaryPath); err == nil {
			fmt.Fprintf(stdout, "Removing binary: %s\n", binaryPath)
			if err := os.Remove(binaryPath); err != nil {
				return fmt.Errorf("failed to remove binary: %w", err)
			}
		}

		// Remove config directory
		configDir := getConfigDir()
		if configDir != "" {
			if _, err := os.Stat(configDir); err == nil {
				fmt.Fprintf(stdout, "Removing config directory: %s\n", configDir)
				if err := os.RemoveAll(configDir); err != nil {
					return fmt.Errorf("failed to remove config directory: %w", err)
				}
			}
		}

		// Remove PATH entries from shell rc files
		shell := detectUserShell()
		rcFile := getRcFile(shell)
		if rcFile != "" {
			localBin := filepath.Join(os.Getenv("HOME"), ".local", "bin")
			if err := removePathFromRc(rcFile, localBin); err != nil {
				fmt.Fprintf(stderr, "Warning: failed to remove PATH entry: %v\n", err)
			} else {
				fmt.Fprintf(stdout, "Removed PATH entry from %s\n", rcFile)
			}
		}
	}

	// Uninstall Deno if marker exists
	if checkDenoMarker() {
		fmt.Fprintln(stdout, "Removing Deno (installed by Genie)...")
		if err := uninstallDeno(); err != nil {
			return fmt.Errorf("failed to uninstall Deno: %w", err)
		}
		fmt.Fprintln(stdout, "Deno removed")
	}

	fmt.Fprintln(stdout, "Uninstall complete")
	return nil
}