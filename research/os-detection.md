# OS Detection Research for Genie Installer Script

## 1. GitHub Release Asset Naming Conventions for Go Binaries

### GoReleaser Default Naming Template
GoReleaser (the standard Go release tool) uses this default archive name template:

```
{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}{{ with .Arm }}v{{ . }}{{ end }}{{ with .Mips }}_{{ . }}{{ end }}{{ if not (eq .Amd64 "v1") }}{{ .Amd64 }}{{ end }}
```

### Common Patterns by Platform

| Platform | GOOS | GOARCH | Example Asset Name |
|----------|------|--------|-------------------|
| Linux x86_64 | linux | amd64 | genie_1.0.0_linux_amd64.tar.gz |
| Linux ARM64 | linux | arm64 | genie_1.0.0_linux_arm64.tar.gz |
| Linux ARMv7 | linux | arm (GOARM=7) | genie_1.0.0_linux_armv7.tar.gz |
| Linux ARMv6 | linux | arm (GOARM=6) | genie_1.0.0_linux_armv6.tar.gz |
| macOS Intel | darwin | amd64 | genie_1.0.0_darwin_amd64.tar.gz |
| macOS Apple Silicon | darwin | arm64 | genie_1.0.0_darwin_arm64.tar.gz |
| Windows x86_64 | windows | amd64 | genie_1.0.0_windows_amd64.zip |
| Windows ARM64 | windows | arm64 | genie_1.0.0_windows_arm64.zip |

**Key Observations:**
- Uses `darwin` not `macos` for macOS (matches Go's GOOS)
- Uses `linux` for Linux
- Uses `windows` for Windows
- Archive format: `.tar.gz` for Unix, `.zip` for Windows
- ARM variants include GOARM version suffix (`v6`, `v7`)
- amd64 includes microarchitecture suffix only if not `v1` (e.g., `_v2`, `_v3`)

**Source:** [GoReleaser Archives Documentation](https://goreleaser.com/customization/package/archives/) - `name_template` default

### Real-World Example: GitHub CLI (cli/cli)
From [GitHub CLI releases](https://github.com/cli/cli/releases):
```
gh_2.102.0_linux_386.tar.gz
gh_2.102.0_linux_amd64.tar.gz
gh_2.102.0_linux_arm64.tar.gz
gh_2.102.0_darwin_amd64.tar.gz
gh_2.102.0_darwin_arm64.tar.gz
gh_2.102.0_windows_386.zip
gh_2.102.0_windows_amd64.zip
gh_2.102.0_windows_arm64.zip
```
Plus `.deb`, `.rpm` packages and checksums file.

---

## 2. Detecting OS and Architecture in Portable Bash

### OS Detection (`uname -s`)

```bash
# Returns: Linux, Darwin, FreeBSD, OpenBSD, NetBSD, Windows (via MSYS/CYGWIN)
OS="$(uname -s)"

case "$OS" in
    Linux*)     GOOS="linux" ;;
    Darwin*)    GOOS="darwin" ;;
    FreeBSD*)   GOOS="freebsd" ;;
    OpenBSD*)   GOOS="openbsd" ;;
    NetBSD*)    GOOS="netbsd" ;;
    CYGWIN*|MINGW*|MSYS*) GOOS="windows" ;;
    *)          echo "Unsupported OS: $OS"; exit 1 ;;
esac
```

### Architecture Detection (`uname -m`)

```bash
# Returns: x86_64, aarch64, arm64, i386, i686, armv7l, ppc64le, s390x, riscv64
ARCH="$(uname -m)"

case "$ARCH" in
    x86_64|amd64)     GOARCH="amd64" ;;
    aarch64|arm64)    GOARCH="arm64" ;;
    armv7l|armv7)     GOARCH="arm"; GOARM="7" ;;
    armv6l|armv6)     GOARCH="arm"; GOARM="6" ;;
    i386|i686)        GOARCH="386" ;;
    ppc64le)          GOARCH="ppc64le" ;;
    s390x)            GOARCH="s390x" ;;
    riscv64)          GOARCH="riscv64" ;;
    *)                echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac
```

### WSL Detection

**Method 1: Check `/proc/version` (most reliable)**
```bash
is_wsl() {
    case "$(cat /proc/version 2>/dev/null)" in
        *microsoft*|*Microsoft*|*WSL*) return 0 ;;
        *) return 1 ;;
    esac
}
```

**Method 2: Check kernel release (also works)**
```bash
is_wsl() {
    case "$(uname -r)" in
        *microsoft*|*Microsoft*) return 0 ;;
        *) return 1 ;;
    esac
}
```

**Source:** [Docker Install Script](https://github.com/docker/docker-install/blob/master/install.sh) - `is_wsl()` function

### Combined Detection Function

```bash
detect_platform() {
    local os arch
    
    # Detect OS
    case "$(uname -s)" in
        Linux*)     os="linux" ;;
        Darwin*)    os="darwin" ;;
        CYGWIN*|MINGW*|MSYS*) os="windows" ;;
        *)          echo "Error: Unsupported operating system: $(uname -s)" >&2; return 1 ;;
    esac
    
    # Detect architecture
    case "$(uname -m)" in
        x86_64)     arch="amd64" ;;
        aarch64|arm64) arch="arm64" ;;
        armv7l|armv7)  arch="arm"; export GOARM="7" ;;
        armv6l|armv6)  arch="arm"; export GOARM="6" ;;
        i386|i686)     arch="386" ;;
        *)             echo "Error: Unsupported architecture: $(uname -m)" >&2; return 1 ;;
    esac
    
    # WSL special handling
    if [ "$os" = "linux" ] && is_wsl; then
        echo "Note: Running in WSL - using Linux binaries" >&2
    fi
    
    GOOS="$os"
    GOARCH="$arch"
    echo "${GOOS}_${GOARCH}"
}
```

---

## 3. Genie Repository Release Assets

**Current Status:** The Genie repository (github.com/okayest-dev/genie) has **no releases yet**.

**Source:** Checked [https://github.com/okayest-dev/genie/releases](https://github.com/okayest-dev/genie/releases) - "There aren't any releases here"

**Implication:** This will be the first release. Asset naming should follow the GoReleaser conventions documented above.

---

## 4. Best Practices for Early Platform Validation

### Fail Fast with Clear Messages

```bash
validate_platform() {
    local platform="${GOOS}_${GOARCH}"
    
    # Define supported platforms
    local supported=(
        "linux_amd64"
        "linux_arm64"
        "linux_armv7"
        "linux_armv6"
        "darwin_amd64"
        "darwin_arm64"
        "windows_amd64"
        "windows_arm64"
    )
    
    # Check if platform is supported
    local is_supported=false
    for p in "${supported[@]}"; do
        if [ "$platform" = "$p" ]; then
            is_supported=true
            break
        fi
    done
    
    if [ "$is_supported" = false ]; then
        cat >&2 <<EOF
Error: Platform '${platform}' is not supported.

Supported platforms:
  Linux:   amd64, arm64, armv7, armv6
  macOS:   amd64 (Intel), arm64 (Apple Silicon)
  Windows: amd64, arm64

Current detection:
  OS: $(uname -s) -> GOOS=${GOOS}
  Arch: $(uname -m) -> GOARCH=${GOARCH}

Please file an issue at https://github.com/okayest-dev/genie/issues
if you believe this platform should be supported.
EOF
        exit 1
    fi
    
    return 0
}
```

### WSL-Specific Guidance

```bash
if is_wsl; then
    cat >&2 <<EOF
Note: Detected WSL (Windows Subsystem for Linux).

Genie will install the Linux binary, which runs natively in WSL 2.
For Windows-native integration, consider the Windows release instead.

EOF
fi
```

### macOS Version Check (Optional)

```bash
if [ "$GOOS" = "darwin" ]; then
    # Check macOS version for compatibility
    local version="$(sw_vers -productVersion 2>/dev/null | cut -d. -f1)"
    if [ -n "$version" ] && [ "$version" -lt 11 ]; then
        echo "Warning: macOS 11 (Big Sur) or later recommended" >&2
    fi
fi
```

### Dry-Run Support

```bash
DRY_RUN="${DRY_RUN:-0}"

if [ "$DRY_RUN" = "1" ]; then
    echo "[DRY RUN] Would download: ${ASSET_URL}"
    echo "[DRY RUN] Would install to: ${INSTALL_DIR}"
    exit 0
fi
```

**Source:** [Docker Install Script](https://github.com/docker/docker-install/blob/master/install.sh) - demonstrates:
- Early platform validation with clear error messages
- WSL detection with helpful guidance
- Deprecation warnings for unsupported distros
- Dry-run mode for testing
- Actionable error messages with links

### Error Message Template

```
Error: <specific problem>

Context:
  Detected OS: <uname -s output> -> GOOS=<value>
  Detected Arch: <uname -m output> -> GOARCH=<value>
  <WSL status if applicable>

Supported platforms:
  <list>

Suggested action:
  <actionable guidance>

More info: <URL to docs/issues>
```

---

## Summary for Genie Installer Implementation

### Asset URL Construction
```bash
VERSION="${VERSION:-latest}"
BASE_URL="https://github.com/okayest-dev/genie/releases/download"

# For tagged releases:
ASSET_NAME="genie_${VERSION}_${GOOS}_${GOARCH}.tar.gz"
# For Windows:
# ASSET_NAME="genie_${VERSION}_${GOOS}_${GOARCH}.zip"

ASSET_URL="${BASE_URL}/v${VERSION}/${ASSET_NAME}"
# If using "latest" tag:
# ASSET_URL="${BASE_URL}/latest/download/${ASSET_NAME}"
```

### Recommended Platform Matrix (First Class Go Ports)
Based on [Go's first-class ports](https://go.dev/wiki/PortingPolicy#first-class-ports):

| GOOS | GOARCH | Priority |
|------|--------|----------|
| linux | amd64 | Required |
| linux | arm64 | Required |
| darwin | amd64 | Required |
| darwin | arm64 | Required |
| windows | amd64 | Required |
| windows | arm64 | Recommended |
| linux | arm (v7) | Optional |
| linux | arm (v6) | Optional |
| freebsd | amd64 | Optional |
| openbsd | amd64 | Optional |

---

## References

1. **GoReleaser Archive Naming** - https://goreleaser.com/customization/package/archives/#archive-name
2. **GoReleaser Go Builder Options** - https://goreleaser.com/customization/builds/builders/go/#gos-first-class-ports
3. **Go Official Ports** - https://go.dev/doc/install/source#environment
4. **Docker Install Script (WSL detection, platform validation)** - https://github.com/docker/docker-install/blob/master/install.sh
5. **GitHub CLI Release Assets** - https://github.com/cli/cli/releases
6. **Go Bootstrap Script** - https://github.com/golang/go/blob/master/src/bootstrap.bash
