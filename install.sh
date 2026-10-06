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

readonly GITHUB_API="https://api.github.com"
readonly RELEASE_URL="https://github.com/${REPO_OWNER}/${REPO_NAME}/releases"

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

    # Match exact filename (checksum files have format: <hash>  <filename> or <hash> *<filename>)
    want=$(grep -E "[[:space:]]+${basename}$" "${checksums_file}" 2>/dev/null | tr '\t' ' ' | cut -d ' ' -f 1)
    if [ -z "$want" ]; then
        # Try with leading * (some checksum formats)
        want=$(grep -E "[[:space:]]+\\*${basename}$" "${checksums_file}" 2>/dev/null | tr '\t' ' ' | cut -d ' ' -f 1)
    fi
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

detect_existing_version() {
    local install_dir="${1:-${HOME}/.local/bin}"
    local binary_path="${install_dir}/${BINARY_NAME}"
    local existing_version=""

    if [ ! -x "$binary_path" ]; then
        log_debug "No existing binary found at ${binary_path}"
        echo ""
        return 0
    fi

    log_info "Detecting existing installation at ${binary_path}..."

    # Run genie --version to get the version
    existing_version=$("$binary_path" --version 2>/dev/null) || {
        log_warn "Failed to get version from existing binary"
        echo ""
        return 0
    }

    # Strip any whitespace and 'v' prefix
    existing_version=$(echo "$existing_version" | sed -E 's/^[[:space:]]+|[[:space:]]+$//g' | sed -E 's/^v//')

    if [ -z "$existing_version" ]; then
        log_warn "Existing binary returned empty version"
        echo ""
        return 0
    fi

    log_info "Found existing version: ${existing_version}"
    echo "$existing_version"
    return 0
}

prompt_upgrade() {
    local current_version="$1"
    local new_version="$2"

    cat >&2 <<EOF
Genie ${current_version} is installed.
Update to ${new_version}? (y/N)
EOF

    local response=""
    read -r response
    response=$(echo "$response" | tr '[:upper:]' '[:lower:]' | sed -E 's/^[[:space:]]+|[[:space:]]+$//g')

    case "$response" in
        y|yes)
            return 0
            ;;
        *)
            return 1
            ;;
    esac
}

check_and_prompt_upgrade() {
    local new_version="$1"
    local install_dir="${2:-${HOME}/.local/bin}"
    local force="${3:-0}"

    local existing_version
    existing_version=$(detect_existing_version "$install_dir")

    if [ -z "$existing_version" ]; then
        log_debug "No existing installation detected"
        return 0
    fi

    # Compare versions
    if version_gt "$new_version" "$existing_version"; then
        log_info "Newer version available: ${new_version} > ${existing_version}"

        if [ "$force" = "1" ]; then
            log_info "Force flag set, proceeding with upgrade"
            return 0
        fi

        if prompt_upgrade "$existing_version" "$new_version"; then
            log_info "User confirmed upgrade"
            return 0
        else
            log_info "User declined upgrade"
            log_info "Installation cancelled. Existing version ${existing_version} kept."
            exit 0
        fi
    elif version_lt "$new_version" "$existing_version"; then
        log_warn "Target version ${new_version} is older than installed version ${existing_version}"

        if [ "$force" = "1" ]; then
            log_info "Force flag set, proceeding with downgrade"
            return 0
        fi

        if prompt_upgrade "$existing_version" "$new_version"; then
            log_info "User confirmed downgrade"
            return 0
        else
            log_info "User declined downgrade"
            log_info "Installation cancelled. Existing version ${existing_version} kept."
            exit 0
        fi
    else
        log_info "Same version already installed: ${existing_version}"

        if [ "$force" = "1" ]; then
            log_info "Force flag set, reinstalling"
            return 0
        fi

        if prompt_upgrade "$existing_version" "$new_version"; then
            log_info "User confirmed reinstall"
            return 0
        else
            log_info "User declined reinstall"
            log_info "Installation cancelled. Existing version ${existing_version} kept."
            exit 0
        fi
    fi
}

detect_user_shell() {
    local shell_name=""

    # Try to detect from BASH environment variable
    if [ -n "${BASH:-}" ]; then
        shell_name="bash"
    elif [ -n "${ZSH_VERSION:-}" ]; then
        shell_name="zsh"
    elif [ -n "${FISH_VERSION:-}" ]; then
        shell_name="fish"
    else
        # Fallback: check /etc/passwd
        local user_shell
        user_shell=$(getent passwd "${USER:-$(whoami)}" 2>/dev/null | cut -d: -f7)
        if [ -n "$user_shell" ]; then
            shell_name=$(basename "$user_shell")
        fi
    fi

    # Normalize shell name
    case "$shell_name" in
        *bash*) echo "bash" ;;
        *zsh*)  echo "zsh" ;;
        *fish*) echo "fish" ;;
        *)      echo "bash" ;;  # default to bash
    esac
}

get_rc_file() {
    local shell="$1"
    local home_dir="${HOME}"

    case "$shell" in
        bash)
            echo "${home_dir}/.bashrc"
            ;;
        zsh)
            echo "${home_dir}/.zshrc"
            ;;
        fish)
            echo "${home_dir}/.config/fish/config.fish"
            ;;
        *)
            echo "${home_dir}/.bashrc"
            ;;
    esac
}

path_entry_exists() {
    local rc_file="$1"
    local path_entry="$2"

    if [ ! -f "$rc_file" ]; then
        return 1
    fi

    # Check for exact match or prefix match of the path entry
    # We look for the path entry as a whole directory in PATH
    local escaped_entry
    escaped_entry=$(echo "$path_entry" | sed 's/[[\.*^$()+?{|\\]/\\&/g')

    # Check for export PATH="...$path_entry..." or PATH="...$path_entry..."
    # and also fish's set -gx PATH ... $path_entry ...
    if grep -qE "(export[[:space:]]+)?PATH=.*[:\"]${escaped_entry}(:|\"|')|set[[:space:]]+-gx[[:space:]]+PATH[[:space:]]+.*[[:space:]]${escaped_entry}([[:space:]]|$)" "$rc_file" 2>/dev/null; then
        return 0
    fi

    return 1
}

append_path_to_rc() {
    local rc_file="$1"
    local path_entry="$2"
    local shell="$3"

    local path_line=""

    case "$shell" in
        fish)
            # Fish uses: set -gx PATH /existing/path /new/path
            path_line="set -gx PATH \$PATH ${path_entry}"
            ;;
        *)
            # Bash/zsh use: export PATH="$PATH:/new/path"
            path_line="export PATH=\"\$PATH:${path_entry}\""
            ;;
    esac

    # Create rc file if it doesn't exist
    if [ ! -f "$rc_file" ]; then
        mkdir -p "$(dirname "$rc_file")"
        touch "$rc_file"
    fi

    # Append the PATH line
    echo "" >> "$rc_file"
    echo "# Added by Genie installer" >> "$rc_file"
    echo "$path_line" >> "$rc_file"

    log_debug "Appended PATH entry to ${rc_file}"
    return 0
}

setup_path() {
    local install_dir="${1:-${HOME}/.local/bin}"

    local shell
    shell=$(detect_user_shell)

    local rc_file
    rc_file=$(get_rc_file "$shell")

    log_info "Detected shell: ${shell}"
    log_info "Target rc file: ${rc_file}"

    # Check if PATH entry already exists
    if path_entry_exists "$rc_file" "$install_dir"; then
        log_info "PATH already contains ${install_dir} in ${rc_file}"
        return 0
    fi

    log_info "Adding ${install_dir} to PATH in ${rc_file}..."
    append_path_to_rc "$rc_file" "$install_dir" "$shell"

    # Print notification
    cat >&2 <<EOF
Added ${install_dir} to PATH in ${rc_file}.
Run: source ${rc_file}
EOF

    return 0
}


# Deno installation functions

DENO_INSTALL_URL="https://deno.land/install.sh"
DENO_MARKER_FILE=".deno-installed-by-genie"

detect_deno() {
    local deno_path=""

    # Check common installation locations
    local locations=(
        "${HOME}/.deno/bin/deno"
        "/opt/homebrew/bin/deno"
        "/usr/local/bin/deno"
        "/usr/bin/deno"
    )

    for loc in "${locations[@]}"; do
        if [ -x "$loc" ]; then
            # Verify it works
            if "$loc" --version >/dev/null 2>&1; then
                deno_path="$loc"
                break
            fi
        fi
    done

    if [ -n "$deno_path" ]; then
        log_info "Found existing Deno at: ${deno_path}"
        echo "$deno_path"
        return 0
    fi

    log_info "Deno not found in known locations"
    echo ""
    return 0
}

prompt_deno_install() {
    cat >&2 <<EOF
Deno is required for the Genie code tool but was not found on your system.

Install Deno using the official installer? (y/N)
EOF

    local response=""
    read -r response
    response=$(echo "$response" | tr '[:upper:]' '[:lower:]' | sed -E 's/^[[:space:]]+|[[:space:]]+$//g')

    case "$response" in
        y|yes)
            return 0
            ;;
        *)
            return 1
            ;;
    esac
}

install_deno() {
    log_info "Installing Deno via official installer..."

    # Run the official installer
    if command -v curl >/dev/null 2>&1; then
        if ! curl -fsSL "$DENO_INSTALL_URL" | sh; then
            log_error "Deno installation failed"
            return 1
        fi
    elif command -v wget >/dev/null 2>&1; then
        if ! wget -qO- "$DENO_INSTALL_URL" | sh; then
            log_error "Deno installation failed"
            return 1
        fi
    else
        log_error "Neither curl nor wget available for Deno installation"
        return 1
    fi

    # Verify installation
    local deno_path="${HOME}/.deno/bin/deno"
    if [ -x "$deno_path" ] && "$deno_path" --version >/dev/null 2>&1; then
        log_info "Deno installed successfully at ${deno_path}"
        echo "$deno_path"
        return 0
    fi

    log_error "Deno installation verification failed"
    return 1
}

setup_deno_path() {
    local deno_bin_dir="${HOME}/.deno/bin"

    local shell
    shell=$(detect_user_shell)

    local rc_file
    rc_file=$(get_rc_file "$shell")

    # Check if PATH entry already exists
    if path_entry_exists "$rc_file" "$deno_bin_dir"; then
        log_info "Deno PATH already configured in ${rc_file}"
        return 0
    fi

    log_info "Adding Deno PATH (${deno_bin_dir}) to ${rc_file}..."
    append_path_to_rc "$rc_file" "$deno_bin_dir" "$shell"

    # Print notification
    cat >&2 <<EOF
Added ${deno_bin_dir} to PATH in ${rc_file}.
Run: source ${rc_file}
EOF

    return 0
}

create_deno_marker() {
    local config_dir="${XDG_CONFIG_HOME:-${HOME}/.config}/genie"
    mkdir -p "$config_dir"
    touch "${config_dir}/${DENO_MARKER_FILE}"
    log_debug "Created Deno marker file at ${config_dir}/${DENO_MARKER_FILE}"
}

check_deno_marker() {
    local config_dir="${XDG_CONFIG_HOME:-${HOME}/.config}/genie"
    [ -f "${config_dir}/${DENO_MARKER_FILE}" ]
}

run_deno_install_flow() {
    # Skip if Deno already detected
    local existing_deno
    existing_deno=$(detect_deno)
    if [ -n "$existing_deno" ]; then
        log_info "Deno already installed, skipping Deno installation"
        return 0
    fi

    # Prompt user
    if ! prompt_deno_install; then
        log_info "User declined Deno installation. Code tool will not be available."
        return 0
    fi

    # Install Deno
    local deno_path
    deno_path=$(install_deno) || {
        log_error "Deno installation failed"
        return 1
    }
    record_step "deno_install"

    # Setup PATH
    setup_deno_path || {
        log_warn "Failed to configure Deno PATH"
    }
    record_step "deno_path_setup"

    # Create marker file for uninstall tracking
    create_deno_marker
    record_step "deno_marker"

    return 0
}

# Provider definitions (name:description:api_key_env:default_wire)
PROVIDERS=(
    "zen:OpenAI-compatible (big-pickle):OPENAI_API_KEY:openai"
    "openai:Official OpenAI API:OPENAI_API_KEY:openai"
    "anthropic:Anthropic Claude:ANTHROPIC_API_KEY:anthropic"
    "responses:OpenAI Responses API:OPENAI_API_KEY:responses"
    "google:Google Gemini:GOOGLE_API_KEY:google"
    "copilot:GitHub Copilot (OAuth, no API key):none:copilot"
    "bedrock:AWS Bedrock (AWS SDK auth):none:bedrock"
)

get_config_path() {
    local custom_path="${1:-}"
    if [ -n "$custom_path" ]; then
        echo "$custom_path"
        return 0
    fi
    local xdg_config="${XDG_CONFIG_HOME:-${HOME}/.config}"
    echo "${xdg_config}/genie/config.toml"
}

list_providers() {
    local i=1
    echo "Available providers:"
    for p in "${PROVIDERS[@]}"; do
        IFS=':' read -r name desc api_key_env wire <<< "$p"
        printf "  %d) %s - %s\n" "$i" "$name" "$desc"
        if [ "$api_key_env" != "none" ]; then
            printf "      API key env: %s\n" "$api_key_env"
        else
            printf "      Auth: built-in (no API key needed)\n"
        fi
        i=$((i + 1))
    done
}

parse_provider_selection() {
    local input="$1"
    local count=${#PROVIDERS[@]}

    # Try as number
    if [[ "$input" =~ ^[0-9]+$ ]]; then
        if [ "$input" -ge 1 ] && [ "$input" -le "$count" ]; then
            local p="${PROVIDERS[$((input - 1))]}"
            IFS=':' read -r name desc api_key_env wire <<< "$p"
            echo "$name"
            return 0
        fi
    fi

    # Try as name (case-insensitive)
    for p in "${PROVIDERS[@]}"; do
        IFS=':' read -r name desc api_key_env wire <<< "$p"
        if [ "${name,,}" = "${input,,}" ]; then
            echo "$name"
            return 0
        fi
    done

    return 1
}

get_provider_info() {
    local name="$1"
    for p in "${PROVIDERS[@]}"; do
        IFS=':' read -r pname desc api_key_env wire <<< "$p"
        if [ "$pname" = "$name" ]; then
            echo "$desc|$api_key_env|$wire"
            return 0
        fi
    done
    return 1
}

prompt_provider() {
    list_providers
    echo
    while true; do
        read -rp "Select provider [1-${#PROVIDERS[@]} or name]: " selection
        local name
        name=$(parse_provider_selection "$selection")
        if [ -n "$name" ]; then
            echo "$name"
            return 0
        fi
        log_error "Invalid selection. Please enter a number or provider name."
    done
}

prompt_api_key() {
    local provider_name="$1"
    local info
    info=$(get_provider_info "$provider_name") || return 1
    IFS='|' read -r desc api_key_env wire <<< "$info"

    if [ "$api_key_env" = "none" ]; then
        log_info "Provider '$provider_name' uses built-in authentication (no API key needed)"
        return 0
    fi

    log_info "Provider '$provider_name' requires API key via env var: $api_key_env"
    echo "Options:"
    echo "  1) Enter API key now (saved to config.toml)"
    echo "  2) Add to shell rc file (~/.bashrc, ~/.zshrc, etc.)"
    echo "  3) Skip (set manually later)"

    while true; do
        read -rp "Choice [1-3]: " choice
        case "$choice" in
            1)
                read -rp "Enter $api_key_env value: " api_key
                if [ -n "$api_key" ]; then
                    echo "$api_key"
                    return 0
                fi
                log_warn "Empty API key, try again"
                ;;
            2)
                local shell rc_file
                shell=$(detect_user_shell)
                rc_file=$(get_rc_file "$shell")
                read -rp "Enter $api_key_env value: " api_key
                if [ -n "$api_key" ]; then
                    # Append to rc file
                    echo "" >> "$rc_file"
                    echo "# Added by Genie installer for $provider_name" >> "$rc_file"
                    echo "export $api_key_env=\"$api_key\"" >> "$rc_file"
                    log_info "Added $api_key_env to $rc_file. Run: source $rc_file"
                    return 0
                fi
                log_warn "Empty API key, try again"
                ;;
            3)
                log_info "Skipping API key. Set $api_key_env manually before running Genie."
                return 0
                ;;
            *)
                log_error "Invalid choice"
                ;;
        esac
    done
}

generate_config() {
    local provider_name="$1"
    local api_key="${2:-}"
    local config_path="${3:-}"
    local info
    info=$(get_provider_info "$provider_name") || return 1
    IFS='|' read -r desc api_key_env wire <<< "$info"

    local path
    path=$(get_config_path "$config_path")

    local config_dir
    config_dir=$(dirname "$path")
    mkdir -p "$config_dir"

    # Build TOML
    local toml="provider = \"$provider_name\""

    if [ -n "$api_key" ] && [ "$api_key_env" != "none" ]; then
        toml="$toml\n# $api_key_env is expected in environment (set via shell rc or export)"
    fi

    echo -e "$toml" > "$path"
    log_info "Config written to $path"
    echo "$path"
}

show_config_diff() {
    local old_config="$1"
    local new_config="$2"

    if ! command -v diff >/dev/null 2>&1; then
        log_warn "diff not available, cannot show diff"
        return 0
    fi

    echo "Config changes:"
    diff -u <(echo "$old_config") <(echo "$new_config") | sed '1,2d' || true
}

prompt_config_overwrite() {
    local config_path="$1"
    local new_provider="$2"
    local new_api_key="${3:-}"

    if [ ! -f "$config_path" ]; then
        return 0  # No existing config, proceed
    fi

    local old_config
    old_config=$(cat "$config_path" 2>/dev/null)

    local info
    info=$(get_provider_info "$new_provider") || return 1
    IFS='|' read -r desc api_key_env wire <<< "$info"

    local new_toml="provider = \"$new_provider\""
    if [ -n "$new_api_key" ] && [ "$api_key_env" != "none" ]; then
        new_toml="$new_toml\n# $api_key_env is expected in environment (set via shell rc or export)"
    fi

    show_config_diff "$old_config" "$new_toml"
    echo

    while true; do
        read -rp "Overwrite existing config? [y/N]: " choice
        choice=$(echo "$choice" | tr '[:upper:]' '[:lower:]')
        case "$choice" in
            y|yes) return 0 ;;
            n|no|"") return 1 ;;
            *) log_error "Please answer y or n" ;;
        esac
    done
}

run_config_wizard() {
    local custom_config_path="${1:-}"
    local provider_name="${2:-}"
    local api_key="${3:-}"

    # If provider not specified, prompt
    if [ -z "$provider_name" ]; then
        provider_name=$(prompt_provider)
    else
        # Validate provider
        if ! get_provider_info "$provider_name" >/dev/null; then
            log_error "Unknown provider: $provider_name"
            return 1
        fi
    fi

    local info
    info=$(get_provider_info "$provider_name") || return 1
    IFS='|' read -r desc api_key_env wire <<< "$info"

    # If API key not provided and provider needs one, prompt
    if [ -z "$api_key" ] && [ "$api_key_env" != "none" ]; then
        api_key=$(prompt_api_key "$provider_name") || return 1
    fi

    local config_path
    config_path=$(get_config_path "$custom_config_path")

    # Check for existing config
    if [ -f "$config_path" ]; then
        if ! prompt_config_overwrite "$config_path" "$provider_name" "$api_key"; then
            log_info "Config generation cancelled"
            return 1
        fi
    fi

    generate_config "$provider_name" "$api_key" "$custom_config_path"
    return 0
}

construct_release_asset_name() {
    local version="$1"
    local version_no_v="${version#v}"
    local asset_name="genie_${version_no_v}_${GOOS}_${GOARCH}"
    if [ -n "${GOARM:-}" ]; then
        asset_name="${asset_name}v${GOARM}"
    fi
    asset_name="${asset_name}.tar.gz"
    echo "$asset_name"
}

construct_release_url() {
    local version="$1"
    local asset_name
    asset_name=$(construct_release_asset_name "$version")
    echo "${RELEASE_URL}/download/${version}/${asset_name}"
}

construct_checksums_url() {
    local version="$1"
    echo "${RELEASE_URL}/download/${version}/checksums.txt"
}

fetch_latest_version() {
    local api_url="${GITHUB_API}/repos/${REPO_OWNER}/${REPO_NAME}/releases/latest"
    local version=""
    local response=""

    log_info "Fetching latest version from GitHub API..."

    if command -v curl >/dev/null 2>&1; then
        response=$(curl -sSL -H "Accept: application/vnd.github.v3+json" "$api_url" 2>/dev/null) || return 1
    elif command -v wget >/dev/null 2>&1; then
        response=$(wget -qO- --header="Accept: application/vnd.github.v3+json" "$api_url" 2>/dev/null) || return 1
    else
        log_error "Neither curl nor wget available for API request"
        return 1
    fi

    version=$(echo "$response" | grep -o '"tag_name"[[:space:]]*:[[:space:]]*"[^"]*"' | cut -d'"' -f4)

    if [ -z "$version" ]; then
        log_error "Failed to parse latest version from GitHub API response"
        return 1
    fi

    echo "$version"
    return 0
}

download_file() {
    local url="$1"
    local output="$2"
    local description="${3:-file}"

    log_info "Downloading ${description} from ${url}"

    if command -v curl >/dev/null 2>&1; then
        if ! curl -sSL -o "$output" "$url"; then
            log_error "Failed to download ${description} with curl"
            return 1
        fi
    elif command -v wget >/dev/null 2>&1; then
        if ! wget -q -O "$output" "$url"; then
            log_error "Failed to download ${description} with wget"
            return 1
        fi
    else
        log_error "Neither curl nor wget available for download"
        return 1
    fi

    if [ ! -s "$output" ]; then
        log_error "Downloaded ${description} is empty"
        return 1
    fi

    log_debug "Downloaded ${description} to ${output} ($(wc -c < "$output") bytes)"
    return 0
}

download_release_assets() {
    local version="$1"
    local asset_url checksums_url asset_file checksums_file

    asset_url=$(construct_release_url "$version")
    checksums_url=$(construct_checksums_url "$version")

    asset_file="${TMP_DIR}/$(construct_release_asset_name "$version")"
    checksums_file="${TMP_DIR}/checksums.txt"

    if ! download_file "$asset_url" "$asset_file" "binary archive"; then
        return 1
    fi
    record_step "download_asset"

    if ! download_file "$checksums_url" "$checksums_file" "checksums file"; then
        return 1
    fi
    record_step "download_checksums"

    echo "$asset_file $checksums_file"
    return 0
}

verify_asset_checksum() {
    local asset_file="$1"
    local checksums_file="$2"

    log_info "Verifying SHA256 checksum..."
    if ! hash_sha256_verify "$asset_file" "$checksums_file"; then
        log_error "Checksum verification failed"
        return 1
    fi
    log_info "Checksum verification passed"
    record_step "verify_checksum"
    return 0
}

extract_and_install() {
    local asset_file="$1"
    local install_dir="${2:-${HOME}/.local/bin}"
    local binary_name="${BINARY_NAME}"
    local extracted_binary

    log_info "Extracting archive..."

    if ! tar -xzf "$asset_file" -C "$TMP_DIR" 2>/dev/null; then
        log_error "Failed to extract archive"
        return 1
    fi
    record_step "extract"

    extracted_binary="${TMP_DIR}/${binary_name}"
    if [ ! -f "$extracted_binary" ]; then
        log_error "Binary not found in extracted archive: ${extracted_binary}"
        return 1
    fi

    log_info "Installing binary to ${install_dir}..."

    if [ ! -d "$install_dir" ]; then
        log_info "Creating install directory: ${install_dir}"
        mkdir -p "$install_dir" || {
            log_error "Failed to create install directory: ${install_dir}"
            return 1
        }
    fi
    record_step "create_install_dir"

    local dest_binary="${install_dir}/${binary_name}"
    local temp_dest="${dest_binary}.tmp"

    # Use cp + rm for cross-filesystem safety, then atomic mv
    if ! cp "$extracted_binary" "$temp_dest"; then
        log_error "Failed to copy binary to temporary destination"
        return 1
    fi
    record_step "copy_binary"

    if ! chmod +x "$temp_dest"; then
        log_error "Failed to make binary executable"
        rm -f "$temp_dest"
        return 1
    fi
    record_step "chmod_binary"

    if ! mv "$temp_dest" "$dest_binary"; then
        log_error "Failed to atomically install binary"
        rm -f "$temp_dest"
        return 1
    fi
    record_step "atomic_install"

    log_info "Binary installed successfully to ${dest_binary}"
    return 0
}

fallback_go_install() {
    local version="$1"

    log_warn "Release download failed, falling back to 'go install'..."

    # Use version without 'v' prefix for go install
    local go_version="${version#v}"
    local install_cmd="go install github.com/${REPO_OWNER}/${REPO_NAME}/cmd/${BINARY_NAME}@${go_version}"

    log_info "Running: ${install_cmd}"

    if $install_cmd; then
        log_info "Fallback installation via 'go install' succeeded"
        return 0
    else
        log_error "Fallback installation via 'go install' failed"
        return 1
    fi
}

install_binary() {
    local version="$1"
    local install_dir="${2:-${HOME}/.local/bin}"
    local asset_file checksums_file

    log_info "Downloading release assets for version ${version}..."

    local download_result
    download_result=$(download_release_assets "$version") || {
        log_warn "Failed to download release assets for version ${version}"
        return 1
    }

    read -r asset_file checksums_file <<< "$download_result"
    if [ -z "$asset_file" ] || [ -z "$checksums_file" ]; then
        log_error "Failed to get downloaded file paths"
        return 1
    fi

    if ! verify_asset_checksum "$asset_file" "$checksums_file"; then
        return 1
    fi

    if ! extract_and_install "$asset_file" "$install_dir"; then
        return 1
    fi

    return 0
}

usage() {
    cat <<EOF
Genie Installer v${SCRIPT_VERSION}

Usage: $0 [OPTIONS]

Options:
  -v, --version VERSION    Install specific version (default: latest)
  -d, --dir DIR            Installation directory (default: ~/.local/bin)
  -f, --force              Force installation (skip upgrade prompt, allow downgrade/reinstall)
  -h, --help               Show this help message
  --dry-run                Show what would be done without making changes
  --debug                  Enable debug logging
  --config-wizard          Run interactive config.toml creation after install
  --provider PROVIDER      Provider to configure (with --config-wizard)
  --api-key KEY            API key for provider (with --config-wizard)
  --config-path PATH       Custom config.toml path (default: ~/.config/genie/config.toml)
  --no-deno                Skip Deno installation

Environment Variables:
  GENIE_INSTALL_DIR        Installation directory (default: ~/.local/bin)
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
    local install_dir="${GENIE_INSTALL_DIR:-${HOME}/.local/bin}"
    local force=0
    local dry_run=0
    local config_wizard=0
    local config_provider=""
    local config_api_key=""
    local config_path=""
    local no_deno=0

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
            --config-wizard)
                config_wizard=1
                shift
                ;;
            --provider)
                config_provider="$2"
                shift 2
                ;;
            --api-key)
                config_api_key="$2"
                shift 2
                ;;
            --config-path)
                config_path="$2"
                shift 2
                ;;
            --no-deno)
                no_deno=1
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

    if [ "$version" = "latest" ]; then
        log_info "Resolving latest version from GitHub..."
        version=$(fetch_latest_version) || {
            log_error "Failed to fetch latest version from GitHub API"
            die $EXIT_DOWNLOAD "Cannot resolve latest version"
        }
        log_info "Resolved version: ${version}"
    fi

    # Check for existing installation and prompt for upgrade if needed
    check_and_prompt_upgrade "$version" "$install_dir" "$force"

    if ! install_binary "$version" "$install_dir"; then
        log_warn "Release installation failed for version ${version}"
        if ! fallback_go_install "$version"; then
            die $EXIT_INSTALL "Both release download and go install fallback failed"
        fi
    fi

    # Set up PATH if not already configured
    setup_path "$install_dir"

    # Run config wizard if requested
    if [ "$config_wizard" = "1" ]; then
        log_info "Running config wizard..."
        run_config_wizard "$config_path" "$config_provider" "$config_api_key" || {
            log_warn "Config wizard cancelled or failed"
        }
    fi

    # Run Deno installation flow if not disabled
    if [ "$no_deno" = "0" ]; then
        log_info "Checking for Deno..."
        run_deno_install_flow || {
            log_warn "Deno installation skipped or failed"
        }
    fi

    log_info "Installation complete!"
    log_info "Binary installed to: ${install_dir}/${BINARY_NAME}"
    log_info "Ensure ${install_dir} is in your PATH"
}

if [ "${BASH_SOURCE[0]}" = "${0}" ]; then
    main "$@"
fi