# Copilot Wire Plugin

GitHub Copilot wire plugin for genie. Connects to Copilot via OAuth token exchange and OpenAI-compatible API.

## Setup

### 1. GitHub Authentication

The plugin reads your GitHub OAuth token from `~/.config/github-copilot/hosts.json`. This file is created when you authenticate via GitHub CLI (`gh auth login`) or VS Code.

```bash
# Ensure you're authenticated
gh auth login

# Verify token exists
cat ~/.config/github-copilot/hosts.json
```

The host key in `hosts.json` is `github.com` by default. For GitHub Enterprise, see GHE Support below.

### 2. Configuration

Place a `config.toml` next to the plugin binary:

```toml
domain = ""     # GitHub Enterprise domain (empty = github.com)
```

### 3. Environment Variable Overrides

Env vars take precedence over `config.toml`:

| Variable | Description |
|----------|-------------|
| `GENIE_COPILOT_DOMAIN` | GitHub Enterprise domain (e.g., `github.example.com`) |

Standard GitHub env vars are also supported:

| Variable | Description |
|----------|-------------|
| `GH_HOST` | GitHub host (used by gh CLI) |

### Precedence

```
env vars (GENIE_COPILOT_*) > config.toml > ~/.config/github-copilot/hosts.json > defaults
```

### GHE Support

For GitHub Enterprise, create `~/.config/genie/plugins/copilot/config.toml`:

```toml
domain = "github.example.com"
```

The plugin will use `https://api.github.example.com/copilot_internal/v2/token` for token exchange and read the matching host key from `~/.config/github-copilot/hosts.json`. Without this file, the plugin defaults to `github.com`.

## Models

Available models (use as the model ID in `genie -p`):

- `gpt-5.6` — GPT 5.6 (Luna/Sol/Terra)
- `gpt-5.5` — GPT 5.5
- `gpt-5.4` — GPT 5.4
- `gpt-5.4-mini` — GPT 5.4 Mini
- `gpt-5.4-nano` — GPT 5.4 Nano
- `gpt-5.3-codex` — GPT 5.3 Codex (requires Responses API)
- `gpt-5.2-codex` — GPT 5.2 Codex (requires Responses API)
- `gpt-5-mini` — GPT 5 Mini
- `gpt-4o` — GPT-4o
- `claude-opus-5` — Claude Opus 5
- `claude-opus-4.8` — Claude Opus 4.8
- `claude-opus-4.7` — Claude Opus 4.7
- `claude-sonnet-4.6` — Claude Sonnet 4.6
- `claude-sonnet-4.5` — Claude Sonnet 4.5
- `claude-haiku-4.5` — Claude Haiku 4.5
- `claude-fable-5` — Claude Fable 5
- `gemini-3.7-flash` — Gemini 3.7 Flash
- `gemini-3.6-pro` — Gemini 3.6 Pro
- `gemini-3.5-pro` — Gemini 3.5 Pro
- `gemini-3.5-flash` — Gemini 3.5 Flash

Query `GET /v1/models` with a valid token to get the current list from the API.

## Usage

```bash
# With default github.com
GENIE_MODEL=gpt-4o GENIE_PROVIDER=copilot genie -p "hello"

# With GitHub Enterprise
GENIE_MODEL=gpt-4o GENIE_PROVIDER=copilot genie -p "hello"
```

## Install

Download the latest release from the plugin repository:

```bash
# Linux/macOS
curl -fsSL https://github.com/okayest-dev/genie-copilot/releases/latest/download/copilot-linux-amd64 -o ~/.config/genie/plugins/copilot/copilot
chmod +x ~/.config/genie/plugins/copilot/copilot

# Create config.toml next to binary (optional, for GHE)
cat > ~/.config/genie/plugins/copilot/config.toml <<'EOF'
domain = "github.example.com"
EOF
```

## Troubleshooting

### "model not supported" (400)

Ensure you're sending the required identity headers. The plugin sends:
- `Editor-Version: genie/0.1.0`
- `Editor-Plugin-Version: genie-copilot/0.1.0`
- `Copilot-Integration-Id: vscode-chat`
- `User-Agent: GithubCopilot/genie-0.1.0`

### "Unauthorized" (401)

Token expired. The plugin auto-refreshes 5 minutes before expiry. Check that `~/.config/github-copilot/hosts.json` has a valid `OAuthToken`.

### "Forbidden" (403)

Subscription issue or missing Business/Enterprise headers. Ensure you have an active Copilot subscription.

### Token exchange fails

For GHE, verify:
1. `domain` in config.toml matches your GHE domain
2. `hosts.json` has an entry for that domain with `OAuthToken`
3. Network access to `https://api.<domain>/copilot_internal/v2/token`