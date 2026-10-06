# Self-Update Patterns for CLI Tools

## 1. GitHub CLI (gh) Self-Update Approach

### Overview
GitHub CLI **does not implement binary self-replacement**. Instead, it uses a **notification-only approach**:

1. **Check-for-update logic** (`internal/update/update.go`):
   - Checks for updates every 24 hours (stored in `state.yml`)
   - Uses GitHub API: `GET /repos/{owner}/{repo}/releases/latest`
   - Only returns **non-prerelease, non-draft** releases
   - Compares versions using `hashicorp/go-version`

2. **Update notification**:
   - If update available, shows message: "A new release of gh is available: X.Y.Z → A.B.C"
   - Does **not** perform binary replacement
   - Directs users to package managers or manual download

3. **Package manager preference**:
   ```bash
   # gh recommends:
   brew upgrade gh          # macOS/Linux
   winget upgrade GitHub.cli # Windows
   scoop update gh           # Windows
   choco upgrade gh          # Windows
   ```

### Key Implementation Details
```go
// State persisted in ~/.config/gh/state.yml
type StateEntry struct {
    CheckedForUpdateAt time.Time
    LatestRelease      ReleaseInfo
}

// Only checks if terminal + not in CI + not disabled
func ShouldCheckForUpdate() bool {
    if os.Getenv("GH_NO_UPDATE_NOTIFIER") != "" { return false }
    if os.Getenv("CODESPACES") != "" { return false }
    return !ci.IsCI() && IsTerminal(os.Stdout) && IsTerminal(os.Stderr)
}
```

### API Usage
- **Latest stable**: `GET /repos/cli/cli/releases/latest` (excludes prereleases)
- **All releases**: `GET /repos/cli/cli/releases` (includes prereleases, drafts)
- **By tag**: `GET /repos/cli/cli/releases/tags/{tag}`

---

## 2. Go-Based CLI Self-Update Libraries

### Popular Libraries

| Library | Stars | Features | Notes |
|---------|-------|----------|-------|
| **sanbornm/go-selfupdate** | 1.7k | Binary diffs (bsdiff), custom server, cross-platform | Requires custom update server |
| **rhysd/go-github-selfupdate** | 646 | GitHub Releases, rollback, checksums, signatures, GHE | Most popular for GitHub |
| **creativeprojects/go-selfupdate** | 149 | GitHub/Gitea/GitLab/HTTP, ARM support, checksums | Active fork with more providers |
| **inconshreveable/go-update** | Core | Binary patching, code signing, Windows support | Low-level, used by others |

### rhysd/go-github-selfupdate (Most Common)

```go
import (
    "github.com/blang/semver"
    "github.com/rhysd/go-github-selfupdate/selfupdate"
)

func doSelfUpdate() {
    v := semver.MustParse("1.2.3")
    latest, err := selfupdate.UpdateSelf(v, "owner/repo")
    if err != nil { log.Fatal(err) }
    
    if latest.Version.Equals(v) {
        log.Println("Already latest")
    } else {
        log.Printf("Updated to %s\n%s", latest.Version, latest.ReleaseNotes)
    }
}
```

**Features**:
- Detects latest release via GitHub API
- Downloads correct binary for OS/arch
- **Atomic replacement** with rollback on failure
- Supports `.zip`, `.tar.gz`, `.tar.xz`
- Validates SHA256/ECDSA signatures
- Supports GitHub Enterprise
- Uses `inconshreveable/go-update` for binary replacement

### creativeprojects/go-selfupdate (Modern Fork)

```go
import "github.com/creativeprojects/go-selfupdate"

func update() error {
    source, _ := selfupdate.NewGitHubSource(selfupdate.GitHubConfig{})
    updater, _ := selfupdate.NewUpdater(selfupdate.Config{
        Source: source,
        Validator: &selfupdate.ChecksumValidator{UniqueFilename: "checksums.txt"},
    })
    
    release, found, _ := updater.DetectLatest(ctx, selfupdate.ParseSlug("owner/repo"))
    if !found || release.LessOrEqual(currentVersion) { return nil }
    
    exe, _ := selfupdate.ExecutablePath()
    return updater.UpdateTo(ctx, release, exe)
}
```

**Improvements over rhysd**:
- Context support for cancellation
- Multiple providers (GitHub, Gitea, GitLab, HTTP)
- ARM architecture detection (armv5/armv6/armv7)
- macOS universal binary fallback
- Single checksum file support (goreleaser style)
- Better error wrapping with `errors.Is()`

---

## 3. Package Manager Detection Patterns (Bash)

### Detection Strategy

```bash
detect_package_manager() {
    if command -v brew >/dev/null 2>&1; then
        echo "homebrew"
    elif command -v apt-get >/dev/null 2>&1; then
        echo "apt"
    elif command -v dnf >/dev/null 2>&1; then
        echo "dnf"
    elif command -v yum >/dev/null 2>&1; then
        echo "yum"
    elif command -v pacman >/dev/null 2>&1; then
        echo "pacman"
    elif command -v apk >/dev/null 2>&1; then
        echo "apk"
    elif command -v zypper >/dev/null 2>&1; then
        echo "zypper"
    elif command -v emerge >/dev/null 2>&1; then
        echo "portage"
    elif command -v nix-env >/dev/null 2>&1; then
        echo "nix"
    else
        echo "unknown"
    fi
}
```

### Installation Commands by Manager

```bash
get_install_cmd() {
    local pm="$1"
    local pkg="$2"
    
    case "$pm" in
        homebrew)   echo "brew install $pkg" ;;
        apt)        echo "apt-get update && apt-get install -y $pkg" ;;
        dnf)        echo "dnf install -y $pkg" ;;
        yum)        echo "yum install -y $pkg" ;;
        pacman)     echo "pacman -S --noconfirm $pkg" ;;
        apk)        echo "apk add $pkg" ;;
        zypper)     echo "zypper install -y $pkg" ;;
        portage)    echo "emerge $pkg" ;;
        nix)        echo "nix-env -iA nixpkgs.$pkg" ;;
        *)          return 1 ;;
    esac
}
```

### Update Commands by Manager

```bash
get_update_cmd() {
    local pm="$1"
    local pkg="$2"
    
    case "$pm" in
        homebrew)   echo "brew upgrade $pkg" ;;
        apt)        echo "apt-get update && apt-get install --only-upgrade -y $pkg" ;;
        dnf)        echo "dnf upgrade -y $pkg" ;;
        yum)        echo "yum update -y $pkg" ;;
        pacman)     echo "pacman -Syu --noconfirm $pkg" ;;
        apk)        echo "apk upgrade $pkg" ;;
        zypper)     echo "zypper update -y $pkg" ;;
        portage)    echo "emerge -u $pkg" ;;
        nix)        echo "nix-env -u $pkg" ;;
        *)          return 1 ;;
    esac
}
```

### Detect If Installed via Package Manager

```bash
# Check if binary path matches package manager prefix
is_package_manager_install() {
    local bin_path="$(command -v "$1")"
    local pm="$(detect_package_manager)"
    
    case "$pm" in
        homebrew)  [[ "$bin_path" == "/opt/homebrew/bin/"* ]] || [[ "$bin_path" == "/home/linuxbrew/.linuxbrew/bin/"* ]] ;;
        apt)       [[ "$bin_path" == "/usr/bin/"* ]] ;;
        dnf|yum)   [[ "$bin_path" == "/usr/bin/"* ]] ;;
        pacman)    [[ "$bin_path" == "/usr/bin/"* ]] ;;
        apk)       [[ "$bin_path" == "/usr/bin/"* ]] ;;
        *)         return 1 ;;
    esac
}
```

---

## 4. Binary Self-Replacement Atomic Patterns

### Core Pattern (inconshreveable/go-update)

```go
import "github.com/inconshreveable/go-update"

func applyUpdate(reader io.Reader) error {
    return update.Apply(reader, update.Options{
        TargetPath:     "/path/to/binary",  // optional, defaults to os.Executable()
        TargetMode:     0755,
        Checksum:       expectedSHA256,     // optional verification
        Signature:      publicKey,           // optional code signing
        OldSavePath:    "/path/to/backup",   // rollback location
    })
}
```

### Atomic Replacement Algorithm

1. **Download** new binary to temp file
2. **Verify** checksum/signature
3. **Create backup** of current binary (rename to `.old` or temp)
4. **Write new binary** to temp location
5. **Atomic rename** temp → target (POSIX `rename()` is atomic)
6. **On failure**: restore from backup
7. **On success**: remove backup

### go-update Implementation Details

```go
// From github.com/inconshreveable/go-update/update.go
func Apply(r io.Reader, options Options) error {
    // 1. Create temp file in same directory (for atomic rename)
    tmpfile, err := ioutil.TempFile(filepath.Dir(targetPath), "update-")
    
    // 2. Copy with optional checksum verification
    hasher := sha256.New()
    tee := io.TeeReader(r, hasher)
    _, err = io.Copy(tmpfile, tee)
    
    // 3. Verify checksum if provided
    if options.Checksum != nil && !bytes.Equal(hasher.Sum(nil), options.Checksum) {
        return ErrChecksumMismatch
    }
    
    // 4. Make executable
    tmpfile.Chmod(options.TargetMode)
    tmpfile.Close()
    
    // 5. Backup current binary
    backupPath := targetPath + ".old"
    os.Rename(targetPath, backupPath)
    
    // 6. Atomic rename
    err = os.Rename(tmpfile.Name(), targetPath)
    if err != nil {
        // Rollback on failure
        os.Rename(backupPath, targetPath)
        return err
    }
    
    // 7. Cleanup backup
    os.Remove(backupPath)
    return nil
}
```

### Windows Considerations

```go
// Windows requires special handling - cannot replace running executable
// Solution: Use rename + schedule replacement on next boot
// or use a launcher/bootstrap executable

func applyUpdateWindows(reader io.Reader) error {
    // Write to temp file
    tmpPath := targetPath + ".new"
    // ... write and verify ...
    
    // Use MoveFileEx with MOVEFILE_DELAY_UNTIL_REBOOT
    // Or use a separate updater process
    return windows.MoveFileEx(tmpPath, targetPath, windows.MOVEFILE_REPLACE_EXISTING)
}
```

---

## 5. GitHub API for Releases with Pre-Release Support

### Key Endpoints

| Endpoint | Description | Includes Prereleases? |
|----------|-------------|----------------------|
| `GET /repos/{owner}/{repo}/releases/latest` | Latest **stable** release | ❌ No |
| `GET /repos/{owner}/{repo}/releases` | All releases (paginated) | ✅ Yes |
| `GET /repos/{owner}/{repo}/releases/tags/{tag}` | Specific tag | N/A |

### Release Object Fields

```json
{
  "tag_name": "v1.2.3",
  "name": "Release v1.2.3",
  "body": "Release notes...",
  "draft": false,
  "prerelease": false,
  "created_at": "2024-01-15T10:30:00Z",
  "published_at": "2024-01-15T10:30:00Z",
  "assets": [
    {
      "name": "myapp_linux_amd64.tar.gz",
      "browser_download_url": "https://github.com/.../myapp_linux_amd64.tar.gz",
      "size": 5242880,
      "digest": "sha256:...",
      "content_type": "application/gzip"
    }
  ]
}
```

### Fetching Pre-Releases

```go
func getLatestReleaseIncludePre(client *http.Client, owner, repo string, includePre bool) (*Release, error) {
    url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases", owner, repo)
    req, _ := http.NewRequest("GET", url, nil)
    req.Header.Set("Accept", "application/vnd.github+json")
    
    resp, err := client.Do(req)
    // ... handle response ...
    
    var releases []Release
    json.NewDecoder(resp.Body).Decode(&releases)
    
    for _, r := range releases {
        if r.Draft { continue }
        if !includePre && r.Prerelease { continue }
        return &r, nil  // First non-draft (and non-pre if filtered) is latest
    }
    return nil, ErrNoReleaseFound
}
```

### Rate Limits & Authentication

```bash
# Unauthenticated: 60 requests/hour
# Authenticated: 5000 requests/hour
# Use GITHUB_TOKEN env var or git config

# In Go:
client := &http.Client{}
req.Header.Set("Authorization", "Bearer "+os.Getenv("GITHUB_TOKEN"))
```

### Goreleaser Integration

```yaml
# .goreleaser.yml
release:
  github:
    owner: myorg
    name: myapp
  # Creates releases with proper asset naming
  # Generates checksums.txt for validation
  # Supports pre-release via --snapshot or prerelease: auto
```

---

## Summary: Recommended Approach for Genie

### For a Go-based CLI like Genie:

1. **Use `creativeprojects/go-selfupdate`** - Most feature-complete, actively maintained
   - Supports GitHub (primary), GitLab, Gitea, HTTP
   - ARM architecture detection
   - Checksum validation (goreleaser compatible)
   - Context support for cancellation

2. **Package manager detection** as fallback:
   - Detect if installed via Homebrew, apt, dnf, etc.
   - Show appropriate upgrade command
   - Skip self-update if package manager detected

3. **GitHub API strategy**:
   - Use `/releases` (not `/releases/latest`) to support pre-releases
   - Filter by `draft: false`, optionally include `prerelease: true`
   - Use semantic version comparison

4. **Atomic binary replacement**:
   - Leverage `inconshreveable/go-update` (used by selfupdate libs)
   - Handles cross-platform atomic rename
   - Rollback on failure

5. **Configuration options**:
   - `--check-only` / `--auto` flags
   - Config file to disable auto-check
   - Pre-release channel opt-in

### Example Implementation Structure

```go
// cmd/selfupdate.go
func runSelfUpdate(cmd *cobra.Command, args []string) error {
    // 1. Check if package manager install
    if isPackageManagerInstall() {
        pm := detectPackageManager()
        fmt.Printf("Installed via %s. Run: %s\n", pm, getUpdateCmd(pm, "genie"))
        return nil
    }
    
    // 2. Configure updater
    source, _ := selfupdate.NewGitHubSource(selfupdate.GitHubConfig{})
    updater, _ := selfupdate.NewUpdater(selfupdate.Config{
        Source:    source,
        Validator: &selfupdate.ChecksumValidator{UniqueFilename: "checksums.txt"},
    })
    
    // 3. Detect latest (include pre-release if flag set)
    includePre := cmd.Flag("pre").Value.String() == "true"
    release, found, err := updater.DetectLatest(ctx, selfupdate.ParseSlug("danjones/genie"))
    // ... filter for pre-release if needed ...
    
    // 4. Compare versions
    if !found || release.LessOrEqual(currentVersion) {
        fmt.Println("Already up to date")
        return nil
    }
    
    // 5. Confirm unless --auto
    if !autoConfirm && !confirm(fmt.Sprintf("Update to %s?", release.Version())) {
        return nil
    }
    
    // 6. Apply update
    exe, _ := selfupdate.ExecutablePath()
    if err := updater.UpdateTo(ctx, release, exe); err != nil {
        return fmt.Errorf("update failed: %w", err)
    }
    
    fmt.Printf("Updated to %s\n", release.Version())
    return nil
}
```

### Release Asset Naming (for goreleaser)

```yaml
# .goreleaser.yml
builds:
  - id: genie
    binary: genie
    main: ./cmd/genie
    goos:
      - linux
      - darwin
      - windows
    goarch:
      - amd64
      - arm64
    # ARM variants
    goarm:
      - 6
      - 7
archives:
  - format: tar.gz
    name_template: "{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}"
    format_overrides:
      - goos: windows
        format: zip
checksum:
  name_template: "checksums.txt"
snapshot:
  version_template: "{{ incpatch .Version }}-next"
changelog:
  sort: asc
  filters:
    exclude:
      - "^docs:"
      - "^test:"
```

---

## References

- [rhysd/go-github-selfupdate](https://github.com/rhysd/go-github-selfupdate)
- [creativeprojects/go-selfupdate](https://github.com/creativeprojects/go-selfupdate)
- [sanbornm/go-selfupdate](https://github.com/sanbornm/go-selfupdate)
- [inconshreveable/go-update](https://github.com/inconshreveable/go-update)
- [GitHub Releases API](https://docs.github.com/en/rest/releases/releases)
- [goreleaser](https://goreleaser.com)
- [Homebrew install script](https://github.com/Homebrew/install)
