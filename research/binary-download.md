# Genie Installer Binary Download and Verification Research

## 1. GitHub Release Asset Download Patterns

### Release Asset URL Construction

GitHub releases use the following URL patterns for downloading assets:

#### Specific Version
```
https://github.com/{owner}/{repo}/releases/download/{tag}/{asset-name}
```

#### Latest Release (Redirect)
```
https://github.com/{owner}/{repo}/releases/latest/download/{asset-name}
```

#### Using GitHub API
- **List releases**: `GET /repos/{owner}/{repo}/releases`
- **Get latest release**: `GET /repos/{owner}/{repo}/releases/latest`
- **Get release by tag**: `GET /repos/{owner}/{repo}/releases/tags/{tag}`
- **List release assets**: `GET /repos/{owner}/{repo}/releases/{release_id}/assets`

The API returns `browser_download_url` for each asset which is the direct download URL.

### Example from GoReleaser v2.18.2 Release
- Release page: https://github.com/goreleaser/goreleaser/releases/tag/v2.18.2
- Binary asset: `https://github.com/goreleaser/goreleaser/releases/download/v2.18.2/goreleaser_Linux_x86_64.tar.gz`
- Checksums: `https://github.com/goreleaser/goreleaser/releases/download/v2.18.2/checksums.txt`

**Source**: [GitHub REST API for Releases](https://docs.github.com/en/rest/releases/releases), [GoReleaser v2.18.2 Release Assets](https://github.com/goreleaser/goreleaser/releases/tag/v2.18.2)

---

## 2. Checksum File Formats Used by GoReleaser

### Single Checksum File (Default: `split: false`)

**File name**: `{project}_{version}_checksums.txt` (configurable via `name_template`)

**Format**: Standard `sha256sum` output format
```
<hash>  <filename>
<hash>  <filename>
...
```

**Example** (from GoReleaser v2.18.2):
```
0a96edc9d9bc594e4a41cc4d59467c182062910ab24d9d1f6dd7b667d32606d3  goreleaser_Linux_x86_64.tar.gz
4bfd26942aa0f78e275cad3e49144a7c5ce2e8a34f7d0e9d34c718125766ba12  goreleaser_Darwin_all.tar.gz
...
```

### Split Checksum Files (When `split: true`)

Each artifact gets its own `.sha256` file:
```
goreleaser_Linux_x86_64.tar.gz.sha256
```

**Content**: Just the hash (no filename)
```
0a96edc9d9bc594e4a41cc4d59467c182062910ab24d9d1f6dd7b667d32606d3
```

### GoReleaser Configuration Options

```yaml
checksum:
  name_template: "{{ .ProjectName }}_{{ .Version }}_checksums.txt"  # Default
  algorithm: sha256  # sha256, sha512, sha1, crc32, md5, etc.
  split: false       # true = one file per artifact
  disable: false
  ids: []            # Specific artifact IDs to include
  extra_files: []    # Additional files to checksum
```

**Source**: [GoReleaser Checksums Documentation](https://goreleaser.com/customization/package/checksum/), [GoReleaser v2.18.2 checksums.txt](https://github.com/goreleaser/goreleaser/releases/download/v2.18.2/checksums.txt)

---

## 3. Verifying Downloads in Bash

### Available Commands by Platform

| Platform | Command |
|----------|---------|
| Linux (GNU coreutils) | `sha256sum file` |
| macOS / BSD | `shasum -a 256 file` |
| Linux (alternative) | `openssl dgst -sha256 file` |
| Any (if installed) | `gsha256sum file` |

### Cross-Platform Verification Function (from client9/shlib)

```bash
hash_sha256() {
  TARGET=${1:-/dev/stdin}
  if is_command gsha256sum; then
    hash=$(gsha256sum "$TARGET") || return 1
    echo "$hash" | cut -d ' ' -f 1
  elif is_command sha256sum; then
    hash=$(sha256sum "$TARGET") || return 1
    echo "$hash" | cut -d ' ' -f 1
  elif is_command shasum; then
    hash=$(shasum -a 256 "$TARGET" 2>/dev/null) || return 1
    echo "$hash" | cut -d ' ' -f 1
  elif is_command openssl; then
    hash=$(openssl dgst -sha256 "$TARGET") || return 1
    echo "$hash" | cut -d ' ' -f 2  # openssl outputs "SHA256(file)= hash"
  else
    return 1
  fi
}
```

### Verifying Against Checksums File

```bash
hash_sha256_verify() {
  TARGET=$1
  checksums=$2
  BASENAME=${TARGET##*/}
  # Extract expected hash for this file from checksums.txt
  want=$(grep "${BASENAME}" "${checksums}" 2>/dev/null | tr '\t' ' ' | cut -d ' ' -f 1)
  if [ -z "$want" ]; then
    return 1
  fi
  got=$(hash_sha256 "$TARGET")
  if [ "$want" != "$got" ]; then
    return 1
  fi
}
```

**Source**: [Helm get-helm-3 script](https://github.com/helm/helm/blob/main/scripts/get-helm-3), [Trivy install.sh](https://github.com/aquasecurity/trivy/blob/main/contrib/install.sh), [client9/shlib](https://github.com/client9/shlib)

---

## 4. GoReleaser Checksum Generation Patterns

### Default Behavior
- Algorithm: SHA256
- Single file: `{project}_{version}_checksums.txt`
- Includes all published binaries, archives, linux packages, and source archives
- Uploaded as release asset

### Configuration Examples

**Single checksum file (default)**:
```yaml
checksum:
  name_template: "checksums.txt"
```

**Split mode (one per artifact)**:
```yaml
checksum:
  split: true
  algorithm: sha256
```

**Custom algorithm**:
```yaml
checksum:
  algorithm: sha512
```

**Include specific artifacts only**:
```yaml
checksum:
  ids:
    - binary
    - archive
```

### Verification in GoReleaser

GoReleaser includes a `verify` section that can verify checksums on release:

```yaml
verify:
  commands:
    - cmd: sha256sum
      args: ["-c", "checksums.txt"]
```

**Source**: [GoReleaser .goreleaser.yaml](https://github.com/goreleaser/goreleaser/blob/main/.goreleaser.yaml), [GoReleaser Checksums Docs](https://goreleaser.com/customization/package/checksum/)

---

## 5. Fallback Strategies When Release Not Found

### Strategy 1: Fallback to `go install`

When GitHub release is unavailable (404, rate limited, etc.), fall back to building from source:

```bash
# Try downloading from GitHub releases first
download_from_github() {
  # ... download logic ...
  return $?
}

# Fallback to go install
install_from_source() {
  local version="${1:-latest}"
  go install "github.com/owner/repo@${version}"
}

# Main flow
if ! download_from_github; then
  echo "Release download failed, falling back to go install..."
  install_from_source "$VERSION"
fi
```

### Strategy 2: Version Resolution Fallback

```bash
# Try to get latest version from GitHub API
get_latest_version() {
  curl -sL "https://api.github.com/repos/owner/repo/releases/latest" \
    | grep '"tag_name"' | sed 's/.*"tag_name": "\(.*\)".*/\1/'
}

# If API fails, use a known version or prompt user
VERSION=$(get_latest_version)
if [ -z "$VERSION" ]; then
  VERSION="v1.0.0"  # Fallback to known version
  echo "Warning: Could not fetch latest version, using $VERSION"
fi
```

### Strategy 3: Multiple Download Sources

```bash
# Primary: GitHub Releases
# Secondary: Custom CDN (e.g., get.project.dev)
# Tertiary: go install

download_binary() {
  local url="$1"
  local dest="$2"
  
  if curl -fL "$url" -o "$dest"; then
    return 0
  fi
  
  # Try alternative CDN
  local alt_url="${url/github.com\/releases\/download/get.project.dev}"
  if curl -fL "$alt_url" -o "$dest"; then
    return 0
  fi
  
  return 1
}
```

**Source**: [Go install documentation](https://go.dev/doc/install), [pkg.go.dev go install](https://pkg.go.dev/cmd/go#hdr-Compile_and_install_packages_and_dependencies), [Trivy install.sh pattern](https://github.com/aquasecurity/trivy/blob/main/contrib/install.sh)

---

## 6. Temporary File Handling and Cleanup

### Secure Temporary Directory Creation

```bash
# Create secure temp directory
TMP_DIR=$(mktemp -d)
# Or with prefix
TMP_DIR=$(mktemp -d -t "genie-install.XXXXXX")

# Set trap for cleanup on exit/error
cleanup() {
  if [ -d "$TMP_DIR" ]; then
    rm -rf "$TMP_DIR"
  fi
}
trap cleanup EXIT
```

### Download to Temp, Verify, Then Move

```bash
download_and_verify() {
  local url="$1"
  local checksum_url="$2"
  local dest="$3"
  
  local tmp_file="$TMP_DIR/$(basename "$url")"
  local tmp_checksum="$TMP_DIR/checksums.txt"
  
  # Download binary
  curl -fL "$url" -o "$tmp_file" || return 1
  
  # Download checksums
  curl -fL "$checksum_url" -o "$tmp_checksum" || return 1
  
  # Verify
  if ! hash_sha256_verify "$tmp_file" "$tmp_checksum"; then
    echo "Checksum verification failed"
    return 1
  fi
  
  # Move to final destination
  mv "$tmp_file" "$dest"
  chmod +x "$dest"
}
```

### Pattern from Helm Installer

```bash
HELM_TMP_ROOT=$(mktemp -dt helm-installer-XXXXXX)

cleanup() {
  if [[ -d "${HELM_TMP_ROOT:-}" ]]; then
    rm -rf "$HELM_TMP_ROOT"
  fi
}

trap cleanup EXIT
```

### Best Practices

1. **Always use `mktemp -d`** for unique, secure temp directories
2. **Set `trap cleanup EXIT`** early to ensure cleanup on any exit
3. **Download to temp first**, verify, then move to destination (atomic)
4. **Clean up on failure** using trap handlers
5. **Use `set -e`** to exit on errors, combined with trap for cleanup

**Source**: [Helm get-helm-3 cleanup](https://github.com/helm/helm/blob/main/scripts/get-helm-3), [Trivy install.sh execute()](https://github.com/aquasecurity/trivy/blob/main/contrib/install.sh)

---

## Summary: Recommended Implementation for Genie

```bash
#!/usr/bin/env bash
set -euo pipefail

# Configuration
REPO_OWNER="genie-org"
REPO_NAME="genie"
BINARY_NAME="genie"
INSTALL_DIR="/usr/local/bin"

# Temp directory with cleanup
TMP_DIR=$(mktemp -d -t "genie-install.XXXXXX")
cleanup() { rm -rf "$TMP_DIR"; }
trap cleanup EXIT

# Cross-platform sha256
hash_sha256() { ... }  # As shown in section 3

# Download and verify
download_binary() {
  local version="$1"
  local os="$2"
  local arch="$3"
  
  local asset="${BINARY_NAME}_${version}_${os}-${arch}.tar.gz"
  local url="https://github.com/${REPO_OWNER}/${REPO_NAME}/releases/download/${version}/${asset}"
  local checksum_url="https://github.com/${REPO_OWNER}/${REPO_NAME}/releases/download/${version}/checksums.txt"
  
  local tmp_file="${TMP_DIR}/${asset}"
  local tmp_checksum="${TMP_DIR}/checksums.txt"
  
  curl -fL "$url" -o "$tmp_file" || return 1
  curl -fL "$checksum_url" -o "$tmp_checksum" || return 1
  
  hash_sha256_verify "$tmp_file" "$tmp_checksum" || return 1
  
  # Extract and install
  tar -xzf "$tmp_file" -C "$TMP_DIR"
  mv "${TMP_DIR}/${BINARY_NAME}" "${INSTALL_DIR}/${BINARY_NAME}"
  chmod +x "${INSTALL_DIR}/${BINARY_NAME}"
}

# Fallback to go install
fallback_go_install() {
  go install "github.com/${REPO_OWNER}/${REPO_NAME}@${VERSION}"
}

# Main
main() {
  detect_os_arch
  resolve_version
  
  if ! download_binary "$VERSION" "$OS" "$ARCH"; then
    echo "Download failed, falling back to go install..."
    fallback_go_install
  fi
}
```

---

## References

1. [GitHub REST API - Releases](https://docs.github.com/en/rest/releases/releases)
2. [GitHub REST API - Release Assets](https://docs.github.com/en/rest/releases/assets)
3. [GoReleaser Checksums Configuration](https://goreleaser.com/customization/package/checksum/)
4. [GoReleaser v2.18.2 Release](https://github.com/goreleaser/goreleaser/releases/tag/v2.18.2)
5. [GoReleaser v2.18.2 checksums.txt](https://github.com/goreleaser/goreleaser/releases/download/v2.18.2/checksums.txt)
6. [Helm get-helm-3 Installer](https://github.com/helm/helm/blob/main/scripts/get-helm-3)
7. [Trivy Install Script](https://github.com/aquasecurity/trivy/blob/main/contrib/install.sh)
8. [client9/shlib](https://github.com/client9/shlib)
9. [Go Install Documentation](https://go.dev/doc/install)
10. [pkg.go.dev - go install](https://pkg.go.dev/cmd/go#hdr-Compile_and_install_packages_and_dependencies)
11. [GoReleaser godownloader (deprecated)](https://github.com/goreleaser/godownloader)
12. [Gruntwork fetch](https://github.com/gruntwork-io/fetch)
