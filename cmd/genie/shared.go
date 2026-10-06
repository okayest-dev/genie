package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"golang.org/x/term"
)

type packageManager string

const (
	pkgHomebrew = "homebrew"
	pkgApt      = "apt"
	pkgDnf      = "dnf"
	pkgPacman   = "pacman"
	pkgUnknown  = "unknown"
)

func detectPackageManager(executablePath string) packageManager {
	absPath, err := filepath.Abs(executablePath)
	if err != nil {
		return pkgUnknown
	}

	// Check Homebrew paths
	if strings.HasPrefix(absPath, "/opt/homebrew/bin/") ||
		strings.HasPrefix(absPath, "/home/linuxbrew/.linuxbrew/bin/") {
		return pkgHomebrew
	}

	// Check system package manager paths
	if strings.HasPrefix(absPath, "/usr/bin/") {
		if _, err := os.Stat("/etc/debian_version"); err == nil {
			return pkgApt
		}
		if _, err := os.Stat("/etc/fedora-release"); err == nil {
			return pkgDnf
		}
		if _, err := os.Stat("/etc/arch-release"); err == nil {
			return pkgPacman
		}
		return pkgApt
	}

	// Check /usr/local/bin - typically manual installs
	if strings.HasPrefix(absPath, "/usr/local/bin/") {
		return pkgUnknown
	}

	return pkgUnknown
}

func getPackageManagerUpgradeCmd(pm packageManager) string {
	switch pm {
	case pkgHomebrew:
		return "brew upgrade genie"
	case pkgApt:
		return "sudo apt update && sudo apt upgrade genie"
	case pkgDnf:
		return "sudo dnf upgrade genie"
	case pkgPacman:
		return "sudo pacman -Syu genie"
	default:
		return ""
	}
}

func getPackageManagerUninstallCmd(pm packageManager) string {
	switch pm {
	case pkgHomebrew:
		return "brew uninstall genie"
	case pkgApt:
		return "sudo apt remove genie"
	case pkgDnf:
		return "sudo dnf remove genie"
	case pkgPacman:
		return "sudo pacman -R genie"
	default:
		return ""
	}
}

func detectUserShell() string {
	if runtime.GOOS == "windows" {
		return "powershell"
	}

	// Try to detect from environment variables
	if os.Getenv("BASH") != "" {
		return "bash"
	}
	if os.Getenv("ZSH_VERSION") != "" {
		return "zsh"
	}
	if os.Getenv("FISH_VERSION") != "" {
		return "fish"
	}

	// Fallback: check SHELL env var
	shell := os.Getenv("SHELL")
	if shell != "" {
		base := filepath.Base(shell)
		switch base {
		case "bash", "zsh", "fish":
			return base
		}
	}

	// Default to bash
	return "bash"
}

func getRcFile(shell string) string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	switch shell {
	case "bash":
		return filepath.Join(homeDir, ".bashrc")
	case "zsh":
		return filepath.Join(homeDir, ".zshrc")
	case "fish":
		return filepath.Join(homeDir, ".config", "fish", "config.fish")
	default:
		return filepath.Join(homeDir, ".bashrc")
	}
}

func isTerminal() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

func promptConfirm(message string) (bool, error) {
	fmt.Print(message + " [y/N]: ")
	var response string
	_, err := fmt.Scanln(&response)
	if err != nil {
		return false, err
	}
	return strings.ToLower(strings.TrimSpace(response)) == "y", nil
}