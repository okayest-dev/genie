#!/usr/bin/env bash

readonly SCRIPT_VERSION="1.0.0"
readonly REPO_OWNER="okayest-dev"
readonly REPO_NAME="genie"
readonly BINARY_NAME="genie"

readonly EXIT_GENERAL=1
readonly EXIT_DOWNLOAD=2
readonly EXIT_VERIFY=3
readonly EXIT_INSTALL=4
readonly EXIT_CONFIG=5
readonly EXIT_PATH=6
readonly EXIT_DENO=7
readonly EXIT_EXISTING=8

GOOS=""
GOARCH=""
GOARM=""
IS_WSL=0
STEPS_COMPLETED=()
TMP_DIR=""

log_info() {
    echo "[INFO] $*" >&2
}

log_warn() {
    echo "[WARN] $*" >&2
}

log_error() {
    echo "[ERROR] $*" >&2
}

log_debug() {
    if [ "${DEBUG:-0}" = "1" ]; then
        echo "[DEBUG] $*" >&2
    fi
}

die() {
    local exit_code="${1:-$EXIT_GENERAL}"
    shift
    log_error "$*"
    exit "$exit_code"
}

cleanup() {
    local exit_code=$?
    if [ -n "$TMP_DIR" ] && [ -d "$TMP_DIR" ]; then
        log_debug "Cleaning up temporary directory: $TMP_DIR"
        rm -rf "$TMP_DIR"
    fi
    if [ ${#STEPS_COMPLETED[@]} -gt 0 ] && [ $exit_code -ne 0 ]; then
        log_info "Rolling back completed steps (in reverse order): ${STEPS_COMPLETED[*]}"
        for ((i=${#STEPS_COMPLETED[@]}-1; i>=0; i--)); do
            rollback_step "${STEPS_COMPLETED[i]}"
        done
    fi
    exit $exit_code
}

trap cleanup EXIT

record_step() {
    STEPS_COMPLETED+=("$1")
    log_debug "Recorded step: $1"
}

rollback_step() {
    local step="$1"
    log_debug "Rolling back step: $step"
    case "$step" in
        "temp_dir")
            if [ -n "$TMP_DIR" ] && [ -d "$TMP_DIR" ]; then
                rm -rf "$TMP_DIR"
                log_debug "Removed temp directory during rollback"
            fi
            ;;
        *)
            log_debug "No rollback action for step: $step"
            ;;
    esac
}

is_wsl() {
    if [ -f /proc/version ]; then
        case "$(cat /proc/version 2>/dev/null)" in
            *microsoft*|*Microsoft*|*WSL*) return 0 ;;
            *) return 1 ;;
        esac
    fi
    return 1
}

detect_platform() {
    local os arch

    case "$(uname -s)" in
        Linux*)     os="linux" ;;
        Darwin*)    os="darwin" ;;
        CYGWIN*|MINGW*|MSYS*) os="windows" ;;
        *)          die $EXIT_GENERAL "Unsupported operating system: $(uname -s)" ;;
    esac

    case "$(uname -m)" in
        x86_64|amd64)     arch="amd64" ;;
        aarch64|arm64)    arch="arm64" ;;
        armv7l|armv7)     arch="arm"; export GOARM="7" ;;
        armv6l|armv6)     arch="arm"; export GOARM="6" ;;
        i386|i686)        arch="386" ;;
        ppc64le)          arch="ppc64le" ;;
        s390x)            arch="s390x" ;;
        riscv64)          arch="riscv64" ;;
        *)                die $EXIT_GENERAL "Unsupported architecture: $(uname -m)" ;;
    esac

    if [ "$os" = "linux" ] && is_wsl; then
        IS_WSL=1
        log_info "Note: Running in WSL - using Linux binaries"
    fi

    GOOS="$os"
    GOARCH="$arch"
    log_info "Detected platform: ${GOOS}_${GOARCH}${GOARM:+v${GOARM}}"
}

validate_platform() {
    local platform="${GOOS}_${GOARCH}${GOARM:+v${GOARM}}"

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
  Arch: $(uname -m) -> GOARCH=${GOARCH}${GOARM:+ (GOARM=${GOARM})}
EOF
        if [ $IS_WSL -eq 1 ]; then
            cat >&2 <<EOF
  WSL: detected

EOF
        fi
        cat >&2 <<EOF
Please file an issue at https://github.com/${REPO_OWNER}/${REPO_NAME}/issues
if you believe this platform should be supported.
EOF
        exit $EXIT_GENERAL
    fi

    log_debug "Platform validation passed: $platform"
}

create_temp_dir() {
    TMP_DIR=$(mktemp -d -t "genie-install.XXXXXX")
    log_debug "Created temporary directory: $TMP_DIR"
    record_step "temp_dir"
}

hash_sha256() {
    local target="${1:-/dev/stdin}"
    local hash=""

    if command -v gsha256sum >/dev/null 2>&1; then
        hash=$(gsha256sum "$target" 2>/dev/null) || return 1
        echo "$hash" | cut -d ' ' -f 1
    elif command -v sha256sum >/dev/null 2>&1; then
        hash=$(sha256sum "$target" 2>/dev/null) || return 1
        echo "$hash" | cut -d ' ' -f 1
    elif command -v shasum >/dev/null 2>&1; then
        hash=$(shasum -a 256 "$target" 2>/dev/null) || return 1
        echo "$hash" | cut -d ' ' -f 1
    elif command -v openssl >/dev/null 2>&1; then
        hash=$(openssl dgst -sha256 "$target" 2>/dev/null) || return 1
        echo "$hash" | cut -d ' ' -f 2
    else
        log_error "No SHA256 utility found (need sha256sum, shasum, or openssl)"
        return 1
    fi
}

hash_sha256_verify() {
    local target="$1"
    local checksums_file="$2"
    local basename="${target##*/}"
    local want=""
    local got=""

    want=$(grep "${basename}" "${checksums_file}" 2>/dev/null | tr '\t' ' ' | cut -d ' ' -f 1)
    if [ -z "$want" ]; then
        log_error "No checksum found for ${basename} in ${checksums_file}"
        return 1
    fi

    got=$(hash_sha256 "$target") || return 1

    if [ "$want" != "$got" ]; then
        log_error "Checksum mismatch for ${basename}"
        log_error "  Expected: ${want}"
        log_error "  Got:      ${got}"
        return 1
    fi

    log_debug "Checksum verified for ${basename}"
    return 0
}

version_compare() {
    local v1="$1"
    local v2="$2"

    v1="${v1#v}"
    v2="${v2#v}"

    local IFS=.
    local -a v1_parts v2_parts
    read -ra v1_parts <<< "$v1"
    read -ra v2_parts <<< "$v2"

    local max_len=${#v1_parts[@]}
    [ ${#v2_parts[@]} -gt $max_len ] && max_len=${#v2_parts[@]}

    for ((i=0; i<max_len; i++)); do
        local p1="${v1_parts[i]:-0}"
        local p2="${v2_parts[i]:-0}"

        local p1_num p1_suffix p2_num p2_suffix
        if [[ "$p1" =~ ^([0-9]+)(.*)$ ]]; then
            p1_num="${BASH_REMATCH[1]}"
            p1_suffix="${BASH_REMATCH[2]}"
        else
            p1_num="0"
            p1_suffix="$p1"
        fi
        if [[ "$p2" =~ ^([0-9]+)(.*)$ ]]; then
            p2_num="${BASH_REMATCH[1]}"
            p2_suffix="${BASH_REMATCH[2]}"
        else
            p2_num="0"
            p2_suffix="$p2"
        fi

        if [ "$p1_num" -gt "$p2_num" ]; then
            return 0
        elif [ "$p1_num" -lt "$p2_num" ]; then
            return 1
        fi

        if [ -n "$p1_suffix" ] && [ -z "$p2_suffix" ]; then
            return 1
        elif [ -z "$p1_suffix" ] && [ -n "$p2_suffix" ]; then
            return 0
        elif [ -n "$p1_suffix" ] && [ -n "$p2_suffix" ]; then
            if [ "$p1_suffix" \> "$p2_suffix" ]; then
                return 0
            elif [ "$p1_suffix" \< "$p2_suffix" ]; then
                return 1
            fi
        fi
    done

    return 2
}

version_gte() {
    version_compare "$1" "$2"
    local result=$?
    [ $result -eq 0 ] || [ $result -eq 2 ]
}

version_gt() {
    version_compare "$1" "$2"
    local result=$?
    [ $result -eq 0 ]
}

version_lte() {
    version_compare "$1" "$2"
    local result=$?
    [ $result -eq 1 ] || [ $result -eq 2 ]
}

version_lt() {
    version_compare "$1" "$2"
    local result=$?
    [ $result -eq 1 ]
}

usage() {
    cat <<EOF
Genie Installer v${SCRIPT_VERSION}

Usage: $0 [OPTIONS]

Options:
  -v, --version VERSION    Install specific version (default: latest)
  -d, --dir DIR            Installation directory (default: /usr/local/bin)
  -f, --force              Force installation even if same version exists
  -h, --help               Show this help message
  --dry-run                Show what would be done without making changes
  --debug                  Enable debug logging

Environment Variables:
  GENIE_INSTALL_DIR        Installation directory (default: /usr/local/bin)
  GENIE_VERSION            Version to install (default: latest)
  DEBUG                    Enable debug output (1/0)

Exit Codes:
  1 - General error
  2 - Download failed
  3 - Checksum verification failed
  4 - Installation failed
  5 - Configuration error
  6 - PATH management error
  7 - Deno installation error
  8 - Existing installation handling error
EOF
}

main() {
    set -euo pipefail
    local version="latest"
    local install_dir="${GENIE_INSTALL_DIR:-/usr/local/bin}"
    local force=0
    local dry_run=0

    while [ $# -gt 0 ]; do
        case "$1" in
            -v|--version)
                version="$2"
                shift 2
                ;;
            -d|--dir)
                install_dir="$2"
                shift 2
                ;;
            -f|--force)
                force=1
                shift
                ;;
            -h|--help)
                usage
                exit 0
                ;;
            --dry-run)
                dry_run=1
                shift
                ;;
            --debug)
                export DEBUG=1
                shift
                ;;
            *)
                die $EXIT_GENERAL "Unknown option: $1"
                ;;
        esac
    done

    log_info "Starting Genie installer v${SCRIPT_VERSION}"
    log_info "Target version: ${version}"
    log_info "Install directory: ${install_dir}"

    if [ "$dry_run" = "1" ]; then
        log_info "DRY RUN MODE - no changes will be made"
    fi

    detect_platform
    validate_platform
    create_temp_dir

    log_info "Platform detection and validation complete"
    log_info "GOOS=${GOOS} GOARCH=${GOARCH}${GOARM:+ GOARM=${GOARM}}"

    if [ "$dry_run" = "1" ]; then
        log_info "DRY RUN: Would proceed with download and installation"
        exit 0
    fi

    log_info "Installer core framework initialization complete"
    log_info "Ready for download, verification, and installation steps"
}

if [ "${BASH_SOURCE[0]}" = "${0}" ]; then
    main "$@"
fi