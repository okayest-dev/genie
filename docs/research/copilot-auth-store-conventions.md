# Copilot Plugin-Owned Auth Store — Design Input Research

Research compiled for the genie copilot wire plugin credential store redesign.
All facts sourced from primary source code, official docs, and the originating
issue. Claims from the issue report are marked **[issue #2]**.

---

## 1. gh + Copilot CLI — Where GitHub Tokens Live

### gh CLI (`github.com/cli/cli`)

**Storage mechanism:** The `gh` CLI uses a two-tier approach:

1. **Preferred:** OS system keyring via `go-keyring` dependency
   ([PR #7043](https://github.com/cli/cli/pull/7043), default since v2.30.0
   [PR #7276](https://github.com/cli/cli/pull/7276)):
   - macOS: Keychain (`keychain` service)
   - Windows: Credential Manager (`wincred`)
   - Linux: libsecret / GNOME Keyring / KWallet (Secret Service D-Bus API)

2. **Fallback:** Plaintext `~/.config/gh/hosts.yml`
   ([source: internal/config/config.go](https://github.com/cli/cli/blob/main/internal/config/config.go)):
   ```yaml
   github.com:
     git_protocol: ssh
     oauth_token: gho_xxx
     user: username
   ```
   The `--insecure-storage` flag explicitly opts in; without it, the keyring is
   attempted first. If the keyring is unavailable, `gh` silently falls back to
   the plaintext file — a long-standing source of user confusion
   ([issue #8954](https://github.com/cli/cli/issues/8954),
   [#10108](https://github.com/cli/cli/issues/10108)).

**Fields tracked:** `oauth_token` (the GitHub OAuth token), `git_protocol`, `user`.
No expiry timestamp is stored — `gh` treats the token as long-lived and relies
on GitHub's own revocation.

**Location override:** `GH_CONFIG_DIR` environment variable changes the base
path. **Source:** [cli/cli discussions/10097](https://github.com/cli/cli/discussions/10097).

### Copilot CLI (`github/copilot-cli`)

**Storage:** System keyring under service name `copilot-cli`
([source: GitHub Docs](https://docs.github.com/en/copilot/how-tos/copilot-cli/set-up-copilot-cli/authenticate-copilot-cli)):

| Platform    | Keychain                |
|-------------|-------------------------|
| macOS       | Keychain Access         |
| Windows     | Credential Manager      |
| Linux       | libsecret (GNOME Keyring, KWallet) |

**Fallback:** Plaintext `~/.copilot/config.json` when keychain is unavailable.

**Credential resolution order:**
1. `COPILOT_GITHUB_TOKEN` env var
2. `GH_TOKEN` env var
3. `GITHUB_TOKEN` env var
4. OAuth token from system keychain
5. GitHub CLI (`gh auth token`) fallback

**Source:** [GitHub Docs — Authenticating Copilot CLI](https://docs.github.com/en/copilot/how-tos/copilot-cli/set-up-copilot-cli/authenticate-copilot-cli).

**Token exchange:** Copilot CLI does **not** manage the Copilot JWT lifecycle
itself — it passes the GitHub token to the Copilot backend which handles
the exchange. The CLI does not store or refresh short-lived Copilot tokens.

### How gh drives Copilot token exchange

The `gh` CLI exposes `gh api` which can call the Copilot token exchange:
```bash
gh api --method GET /copilot_internal/v2/token \
  -H "Accept: application/vnd.github+json" \
  -H "X-GitHub-Api-Version: 2022-11-28" \
  --jq '.token'
```
**Source:** [docs.nodove.com LiteLLM guide](https://docs.nodove.com/development/docker/litellm_copilot_guide/).

**Key takeaway for genie:** Neither `gh` nor Copilot CLI tracks Copilot JWT
expiry — they are not long-running daemons. A plugin that runs as a persistent
server must own both the GitHub token AND the Copilot JWT lifecycle.

---

## 2. opencode — Isolated Auth Store

**Storage file:** `~/.local/share/opencode/auth.json`
([source: opencode.ai docs](https://opencode.ai/docs/cli/)).

**File permissions:** `0600` (owner read/write only)
([source: packages/opencode/src/auth/index.ts](https://github.com/anomalyco/opencode)).

**Stored OAuth shape** ([source: handoff gist](https://gist.github.com/dymoo/c1d68a5f9d16fc7effdafb9bad3c82ea)):
```json
{
  "type": "oauth",
  "access": "<github_access_token>",
  "refresh": "<same_access_token>",
  "expires": 0,
  "enterpriseUrl": "optional_normalized_enterprise_domain"
}
```

**Copilot-specific semantics** ([source: same gist](https://gist.github.com/dymoo/c1d68a5f9d16fc7effdafb9bad3c82ea)):

- `access` and `refresh` store the **same** GitHub OAuth token (typically
  `gho_...`). This is **intentional** — there is no separate refresh token
  in the GitHub device flow for Copilot.
- `expires` is set to `0`. This is **deliberate**: OpenCode does not track
  the short-lived Copilot JWT expiry at the auth layer. The exchange from
  GitHub token → Copilot JWT happens inside the `@ai-sdk/github-copilot`
  transport on each request cycle, not in the auth plugin.
- `enterpriseUrl` is populated for GitHub Enterprise logins and causes
  runtime requests to target `https://copilot-api.<domain>`.

**Refresh behavior:** OpenCode's Copilot plugin does **not** implement a
separate Copilot JWT refresh loop. The GitHub device-flow token is sent as
a bearer on each request, and the SDK layer handles the exchange
transparently. **This is a known limitation** — long-running sessions can
hit 401s when the JWT expires, as reported in
[openclaw/openclaw#31132](https://github.com/openclaw/openclaw/issues/31132)
(not OpenCode itself, but the same class of bug).

**`expires: 0` — intentional or latent bug?**

Based on the source analysis in the handoff gist, `expires: 0` is
**intentional** in the Copilot plugin because:
- The stored token is a GitHub OAuth token (long-lived), not a Copilot JWT
  (short-lived ~25-30 min)
- The shared auth schema uses `expires` to track OAuth token expiry, and
  GitHub device-flow tokens don't have a standard expiry field
- The Copilot JWT exchange + expiry is handled at the transport layer

However, this is a **latent weakness**: because no expiry is tracked at all,
there is no proactive re-authentication when the underlying GitHub token
itself is revoked or goes stale. The issue reporter
**[issue #2]** confirmed this: OpenCode kept working with an isolated store
while `genie`'s shared `apps.json` token had gone stale — but OpenCode would
equally fail silently if its own stored token became invalid.

**Multi-domain keying:** Enterprise logins are stored in the same `auth.json`
file, keyed under a provider ID (`github-copilot`). The `enterpriseUrl`
field distinguishes domains. There is no per-host nesting — it's a flat
provider-keyed map.

---

## 3. litellm and copilot-api Proxy — Token Exchange Policy

### LiteLLM (`github.com/BerriAI/litellm`)

**Storage layout** ([source: litellm/llms/github_copilot/authenticator.py](https://github.com/BerriAI/litellm/blob/main/litellm/llms/github_copilot/authenticator.py)):

```
~/.config/litellm/github_copilot/    (default, configurable via GITHUB_COPILOT_TOKEN_DIR)
├── access-token                     (plain text, the gho_ GitHub OAuth token)
└── api-key.json                     (JSON, the exchanged Copilot API key)
```

**`api-key.json` structure** (derived from source — stored raw exchange response):
```json
{
  "token": "tid=...;exp=...;...",
  "endpoints": { "api": "https://api.githubcopilot.com" },
  "expires_at": 1720000000,
  "refresh_in": 1500
}
```

**Token exchange policy:**
1. On first request, `get_api_key()` reads `api-key.json`
2. If `expires_at > now`, the cached token is returned
3. If expired or missing, `_refresh_api_key()` calls
   `GET /copilot_internal/v2/token` with the GitHub OAuth token
4. The full JSON response (including `token`, `endpoints.api`,
   `expires_at`, `refresh_in`) is written to `api-key.json`
5. On 401 from the token endpoint, litellm retries 3x with the same
   stale access token — **it does NOT invalidate the cached access token**
   ([Bug #25312](https://github.com/BerriAI/litellm/issues/25312))

**API base discovery:** Read from `api_key_info["endpoints"]["api"]` via
`get_api_base()`. This correctly handles different tiers (individual,
business, enterprise).

**GHE support:** Fully configurable via environment variables:
- `GITHUB_COPILOT_API_BASE` — Copilot API endpoint
- `GITHUB_COPILOT_DEVICE_CODE_URL` — device flow URL for the enterprise
- `GITHUB_COPILOT_ACCESS_TOKEN_URL` — token poll URL
- `GITHUB_COPILOT_API_KEY_URL` — token exchange URL

**Source:** [litellm docs](https://docs.litellm.ai/docs/providers/github_copilot).

**Proactive refresh:** None. LiteLLM is request-driven: it checks expiry
on each call and refreshes lazily. No background refresh loop. This works
for a proxy but not for a long-running agent session that needs a token
available *before* the next request.

### copilot-api proxy (`github.com/ericc-ch/copilot-api`)

**Storage layout** ([source: src/lib/paths.ts](https://github.com/ericc-ch/copilot-api/blob/master/src/lib/paths.ts)):
```
~/.local/share/copilot-api/          (XDG data dir)
└── github_token                     (plain text, the GitHub OAuth token)
```
File created with `chmod 0o600`
([source: paths.ts ensureFile](https://github.com/ericc-ch/copilot-api/blob/master/src/lib/paths.ts)).

**Copilot JWT handling** ([source: src/lib/token.ts](https://github.com/ericc-ch/copilot-api/blob/master/src/lib/token.ts)):
- The exchanged Copilot token is held **in-memory only** in `state.copilotToken`
- It is **never persisted** to disk
- Refresh uses the `refresh_in` field from the exchange response

**Proactive refresh strategy:**
```typescript
const refreshInterval = (refresh_in - 60) * 1000  // 60-second buffer
setInterval(async () => {
    const { token } = await getCopilotToken()
    state.copilotToken = token
}, refreshInterval)
```
**Source:** [src/lib/token.ts lines 28-42](https://github.com/ericc-ch/copilot-api/blob/master/src/lib/token.ts).

**Exchange response fields used:**
- `token` — the Copilot JWT
- `refresh_in` — seconds until refresh needed (typically 1500 = 25 min)
- `expires_at` — Unix timestamp (available but the proxy uses `refresh_in` instead)
- `endpoints.api` — the API base URL (the proxy reads this from the exchange
  but doesn't appear to use it for routing — it hardcodes `api.githubcopilot.com`)

### privapps/github-copilot-svcs

**Storage:** Single JSON config file:
```
~/.local/share/github-copilot-svcs/config.json
```

**Fields:**
```json
{
  "github_token": "gho_...",
  "copilot_token": "ghu_...",
  "expires_at": 1720000000,
  "refresh_in": 1500,
  "headers": { ... },
  "timeouts": { ... }
}
```

**Refresh strategy** ([source: GitHub README](https://github.com/privapps/github-copilot-svcs)):
- Refreshes at **20% of token lifetime remaining** (~5-6 min before expiry
  for 25-min tokens)
- Retry: 3 attempts with exponential backoff (2s, 8s, 18s)
- Fallback: full device-flow re-authentication if refresh fails
- Status monitoring: healthy / warning / needs-refresh indicators

**Permissions:** `0700` on the data directory (slightly more restrictive than
the `0600` file convention used elsewhere).

---

## 4. File Conventions Worth Copying

### Format: JSON

Every major client uses JSON for structured credential storage:
- opencode: `auth.json`
- LiteLLM: `api-key.json`
- privapps: `config.json`
- VS Code Copilot: `apps.json` / `hosts.json`

Exception: `gh` uses YAML (`hosts.yml`) and plain-text files for single
tokens. JSON is the clear consensus for structured stores.

### Permission bits

| Tool          | Location                     | Permissions | Source |
|---------------|------------------------------|-------------|--------|
| opencode      | `auth.json`                  | `0600`      | [src/auth/index.ts](https://github.com/anomalyco/opencode) |
| copilot-api   | `github_token`               | `0600`      | [src/lib/paths.ts](https://github.com/ericc-ch/copilot-api/blob/master/src/lib/paths.ts) |
| privapps      | config dir                   | `0700`      | [README](https://github.com/privapps/github-copilot-svcs) |
| gh CLI        | `hosts.yml`                  | `0600` (when written) | [cli/cli issues](https://github.com/cli/cli/issues/7757) |

**Recommendation:** Use `0600` for files, `0700` for the data directory.
This matches the XDG convention and prevents group/other read.

### Atomic writes

No surveyed tool implements true atomic-write (write-to-temp + rename).
Most write directly to the file path. This is a gap worth addressing:
`os.Rename` on Unix is atomic; writing to a `.tmp` file then renaming
provides crash safety.

### Location conventions (XDG)

| Tool          | Base dir                              | XDG class     |
|---------------|---------------------------------------|---------------|
| opencode      | `~/.local/share/opencode/`            | `XDG_DATA_HOME` |
| copilot-api   | `~/.local/share/copilot-api/`         | `XDG_DATA_HOME` |
| privapps      | `~/.local/share/github-copilot-svcs/` | `XDG_DATA_HOME` |
| LiteLLM       | `~/.config/litellm/github_copilot/`   | `XDG_CONFIG_HOME` |
| gh CLI        | `~/.config/gh/`                       | `XDG_CONFIG_HOME` |
| Copilot CLI   | `~/.copilot/`                         | hardcoded     |

**Pattern:** Credentials with a "data" characterization (tokens, keys)
belong in `XDG_DATA_HOME` (`~/.local/share/`). Configuration files in
`XDG_CONFIG_HOME` (`~/.config/`). The convention is somewhat mixed, but
the newer tools (opencode, copilot-api, privapps) converge on
`XDG_DATA_HOME`.

**For genie plugin:** Use `$XDG_DATA_HOME/genie/copilot/` (defaulting to
`~/.local/share/genie/copilot/`).

### Multi-account / multi-domain keying

**The critical requirement** for the genie plugin's environment is support
for `kudelski.ghe.com` (GitHub Enterprise) alongside `github.com`.

**How clients handle this:**

- **opencode:** Flat provider key (`github-copilot`), with `enterpriseUrl`
  field. Single account per provider. To support multiple domains, you'd
  need multiple provider entries.

- **LiteLLM:** Configurable endpoints via env vars — supports one GHE
  domain at a time via `GITHUB_COPILOT_API_BASE`. No multi-domain keying
  in the file layout.

- **gh CLI:** `hosts.yml` is naturally multi-host:
  ```yaml
  github.com:
    oauth_token: gho_...
  enterprise.example.com:
    oauth_token: gho_...
  ```
  This is the cleanest multi-domain pattern.

- **VS Code Copilot / `apps.json`:**
  ```json
  {
    "github.com": { "user": "...", "oauth_token": "..." },
    "enterprise.example.com": { "user": "...", "oauth_token": "..." }
  }
  ```
  **Source:** [GitHub community discussion #47319](https://github.com/orgs/community/discussions/47319),
  [aider docs](https://aider.chat/docs/llms/github.html).

- **privapps:** Single config file, single domain per process instance.

**Recommendation for genie:** Key the store by normalized hostname, like
`gh` and `apps.json` do. Each host entry contains its own GitHub token
and cached Copilot JWT.

---

## 5. Recommended Store Design for genie-plugin-wire-copilot

Based on the cross-client survey, here is a concrete proposal:

### File location

```
$XDG_DATA_HOME/genie/copilot/credentials.json
```
Default: `~/.local/share/genie/copilot/credentials.json`

### File permissions

- Directory: `0700`
- File: `0600`

### File format (JSON)

```json
{
  "version": 1,
  "hosts": {
    "github.com": {
      "oauth_token": "gho_...",
      "copilot_token": "tid=...;exp=...;...",
      "copilot_api_base": "https://api.githubcopilot.com",
      "copilot_token_expires_at": 1720000150,
      "copilot_refresh_in": 1500,
      "user": "login_name",
      "updated_at": "2026-09-06T12:00:00Z"
    },
    "kudelski.ghe.com": {
      "oauth_token": "gho_...",
      "copilot_token": "tid=...;exp=...;...",
      "copilot_api_base": "https://copilot-api.kudelski.ghe.com",
      "copilot_token_expires_at": 1720000150,
      "copilot_refresh_in": 1500,
      "user": "login_name",
      "updated_at": "2026-09-06T12:00:00Z"
    }
  }
}
```

### Field conventions

| Field | Type | Notes |
|-------|------|-------|
| `version` | int | Schema version for future migration |
| `hosts` | map[string]object | Keyed by normalized hostname (no scheme, no trailing slash) |
| `oauth_token` | string | GitHub OAuth token from device flow (`gho_...`) |
| `copilot_token` | string | Short-lived Copilot JWT from `/copilot_internal/v2/token` |
| `copilot_api_base` | string | From `endpoints.api` in exchange response — **must not be hardcoded** |
| `copilot_token_expires_at` | int64 | Unix epoch seconds when Copilot JWT expires |
| `copilot_refresh_in` | int | Seconds from exchange time until refresh needed |
| `user` | string | GitHub login for display/logging |
| `updated_at` | string | ISO 8601 timestamp of last write |

### Lifecycle policies

1. **GitHub token:** Obtained via device flow, stored persistently. No
   expiry tracked (matches all surveyed clients). Re-auth triggered only
   on 401 from token exchange.

2. **Copilot JWT:** Exchanged on every startup and on proactive refresh.
   Proactive refresh triggers at `copilot_token_expires_at - 300` (5 min
   before expiry). On 401 from Copilot API, attempt one immediate
   re-exchange before failing.

3. **Atomic writes:** Write to `credentials.json.tmp`, then
   `os.Rename` to `credentials.json`. Prevents corruption on crash.

4. **Stale GitHub token handling:** On 401 from the token exchange
   endpoint specifically, clear `oauth_token` and surface an actionable
   error: `"Copilot credentials expired for <host>. Run 'genie auth
   login' to re-authenticate."` ([inspired by issue #2](https://github.com/okayest-dev/genie-plugin-wire-copilot/issues/2)).

5. **Multi-domain support:** The `hosts` map naturally supports
   `github.com` and any number of GHE domains. The active host is
   selected by genie config (`provider.domain`).

---

## Sources

| Source | Type | What it provides |
|--------|------|-----------------|
| [cli/cli](https://github.com/cli/cli) | Primary code | gh auth storage: keyring + hosts.yml fallback |
| [github/copilot-cli](https://github.com/github/copilot-cli) + [GitHub Docs](https://docs.github.com/en/copilot/how-tos/copilot-cli/set-up-copilot-cli/authenticate-copilot-cli) | Primary docs | Copilot CLI keychain storage, credential resolution order |
| [anomalyco/opencode](https://github.com/anomalyco/opencode) | Primary code | auth.json layout, Copilot plugin `expires: 0` behavior |
| [dymoo gist](https://gist.github.com/dymoo/c1d68a5f9d16fc7effdafb9bad3c82ea) | Derived from source | Detailed OpenCode auth flow analysis |
| [dymoo gist (2)](https://gist.github.com/dymoo/54fb6cf021dedc254613946ec9527c46) | Derived from source | Handoff doc with Codex vs Copilot comparison |
| [BerriAI/litellm](https://github.com/BerriAI/litellm/blob/main/litellm/llms/github_copilot/authenticator.py) | Primary code | LiteLLM file layout, api-key.json structure, refresh behavior |
| [docs.litellm.ai](https://docs.litellm.ai/docs/providers/github_copilot) | Primary docs | LiteLLM config, GHE env vars, headers |
| [BerriAI/litellm#25312](https://github.com/BerriAI/litellm/issues/25312) | Primary issue | LiteLLM stale token invalidation bug |
| [ericc-ch/copilot-api](https://github.com/ericc-ch/copilot-api) | Primary code | paths.ts, token.ts refresh logic, state.ts |
| [privapps/github-copilot-svcs](https://github.com/privapps/github-copilot-svcs) | Primary code | config.json layout, 20% refresh strategy |
| [openclaw/openclaw#31132](https://github.com/openclaw/openclaw/issues/31132) | Primary issue | Token expiry mid-session bug (same class as genie issue) |
| [anomalyco/opencode#35145](https://github.com/anomalyco/opencode/issues/35145) | Primary issue | `expires: 0` confirmed as known behavior, closed not_planned |
| [copilot-sdk docs](https://docs.github.com/en/copilot/how-tos/copilot-sdk/setup/github-oauth) | Primary docs | Token lifecycle, refresh pattern, multi-user patterns |
| [aider docs](https://aider.chat/docs/llms/github.html) | Primary docs | apps.json format and location |
| [GitHub community#47319](https://github.com/orgs/community/discussions/47319) | Primary discussion | hosts.json / apps.json manual auth format |
| **[issue #2](https://github.com/okayest-dev/genie-plugin-wire-copilot/issues/2)** | Issue report | Current genie failure mode, opencode comparison, `expires: 0` observation |
