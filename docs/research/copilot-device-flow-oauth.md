# GitHub Device-Flow (Device Authorization Grant) OAuth — Facts for a CLI Plugin Login

Research compiled for the genie Copilot wire plugin
(`github.com/okayest-dev/genie-plugin-wire-copilot`). Goal: pin down the real
device-flow OAuth facts that a "login from a CLI plugin" design depends on, for
**both github.com and GitHub Enterprise Server (GHE)**, including how the
resulting token feeds GitHub Copilot's token exchange.

**Scope note.** The *device flow itself* and the *OAuth token-exchange for
Copilot* are two distinct things and are treated separately below. The device
flow is **officially documented** by GitHub (primary source). The Copilot token
endpoint (`/copilot_internal/v2/token` → Copilot JWT) is **undocumented /
unofficial**; every factual claim about it is marked as secondary. Endpoint
URLs and response shapes on the Copilot side can change without notice.

Every claim carries an inline source. Claims that are ambiguous, contested,
undocumented, or where GitHub's stance is unclear are flagged **`[FLAG]`**.

---

## 1. Endpoints & flow

### Starting a flow

```
POST https://github.com/login/device/code
```

For GHE the host becomes the instance's `HOSTNAME`:

```
POST http(s)://HOSTNAME/login/device/code
```

Parameters (both github.com and GHE):

| Parameter  | Required | Notes                                                     |
|------------|----------|-----------------------------------------------------------|
| `client_id`| yes      | Client ID of an OAuth app or GitHub App with device flow enabled |
| `scope`    | no       | Space-delimited scopes requested; device flow commonly requests only `read:user` |

**Source:** github.com — GitHub Docs, "Authorizing OAuth apps", Device flow
(https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/authorizing-oauth-apps#device-flow). GHE — GitHub Docs for Enterprise Server 3.21, same page
(https://docs.github.com/en/enterprise-server@3.21/apps/oauth-apps/building-oauth-apps/authorizing-oauth-apps#device-flow).

Response fields:

| Field             | Type    | Notes                                                            |
|-------------------|---------|------------------------------------------------------------------|
| `device_code`     | string  | 40 characters; used by the client to fetch the token             |
| `user_code`       | string  | 8 characters, hyphen in the middle (e.g. `WDJB-MJHT`); shown to user |
| `verification_uri`| string  | Where the user enters the code: `https://github.com/login/device` (github.com) or `http(s)://HOSTNAME/login/device` (GHE) |
| `expires_in`      | integer | Seconds before both codes expire; default **900 s (15 min)**    |
| `interval`        | integer | Minimum seconds between polls; default **5 s**                  |

**Source:** same GitHub Docs pages (github.com + GHE).

### User verification step

The app displays `user_code` and directs the user to `verification_uri`
(`https://github.com/login/device`, wrapped in the GHE case as `HOSTNAME/login/device`).

**Source:** GitHub Docs, same pages.

### Polling for the token

```
POST https://github.com/login/oauth/access_token
POST http(s)://HOSTNAME/login/oauth/access_token    (GHE)
```

Parameters:

| Parameter   | Required | Notes                                                    |
|-------------|----------|----------------------------------------------------------|
| `client_id` | yes      | The same client_id used to start the flow. **No `client_secret` is needed for device flow.** |
| `device_code`| yes     | From step 1.                                             |
| `grant_type`| yes      | Must be `urn:ietf:params:oauth:grant-type:device_code`.  |

**Source:** GitHub Docs, same pages. The "no client secret required" fact is
explicit: "For the device flow, you must pass your app's client ID... the
`client_secret` is not needed for the device flow." (error code
`incorrect_client_credentials` doc).

On success the response is `access_token` + `token_type=bearer` + `scope`. When
the app uses expiring tokens (or requests `offline_access`), it additionally
returns `refresh_token`, `expires_in`, `refresh_token_expires_in` (see §3).

### Polling error codes (both github.com and GHE)

| Code                         | Meaning / action                                                     |
|------------------------------|----------------------------------------------------------------------|
| `authorization_pending`      | User has not entered the code yet; keep polling at `interval`.       |
| `slow_down`                  | Polled too fast; **+5 s added to the interval**; error returns the new `interval`. |
| `expired_token`              | Documented inconsistently as `token_expired`; the device code expired — start a new flow. |
| `access_denied`              | User clicked cancel; code cannot be reused.                          |
| `unsupported_grant_type`     | `grant_type` not the device-code value.                              |
| `incorrect_client_credentials` | Wrong/missing `client_id`.                                           |
| `incorrect_device_code`      | `device_code` invalid.                                               |
| `device_flow_disabled`       | Device flow not enabled for the app (see §2).                        |

**Source:** GitHub Docs, "Error codes for the device flow" (github.com and GHE).

**When the flow completes:** the app polls until it receives an `access_token`,
or the codes expire (after `expires_in`, 15 minutes). The docs say the user must
enter a valid code within 15 minutes (900 s); after that, request a new device
code. Two rate limits to respect: (a) minimum polling interval (`interval`,
else `slow_down`), and (b) a 50-submissions-per-hour-per-app limit on the
browser verification code page.

**Source:** GitHub Docs, "Rate limits for the device flow" (github.com + GHE).

---

## 2. client_id requirements

### Must belong to a registered app with device flow enabled — yes

The device flow only works if the `client_id` is a **registered OAuth app or
GitHub App** *and* the app has device flow enabled in its settings:

- "Before you can use the device flow to authorize and identify users, you must
  first enable it in your app's settings." — GitHub Docs (github.com + GHE).
- Error `device_flow_disabled` occurs when it is not enabled.
- When creating an OAuth app there is an explicit **"Enable Device Flow"**
  checkbox in app settings. — GitHub Docs, "Creating an OAuth app"
  (https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/creating-an-oauth-app).

**History [`FLAG` — behavior changed]:** Since **March 16, 2022**, device flow
must be manually enabled per app; apps without it get HTTP **400** from the
device-flow endpoints. GitHub made this opt-in deliberately "to reduce the
likelihood of Apps being used in phishing attacks." — GitHub Changelog
"Enable OAuth Device Authentication Flow for Apps"
(https://github.blog/changelog/2022-03-16-enable-oauth-device-authentication-flow-for-apps/).

### Does the app need a callback URL for device flow? — No

GitHub's guidance is explicit that the device flow does **not** use (or need)
redirect URIs:

> "The device flow does not require redirect URIs at all, which means that an
> attacker can use the device flow to remotely impersonate your app as part of
> a phishing attack." — GitHub Docs, "Best practices for creating a GitHub App"
> (https://docs.github.com/en/apps/creating-github-apps/about-creating-github-apps/best-practices-for-creating-a-github-app)

An OAuth app registration still collects a "callback URL," but for device flow
it is not exercised; redirect/callback rules apply only to the web flow.

### Policy on using another product's (unofficial) client_id — contested / unclear

**`[FLAG]`** GitHub does **not document** an explicit policy forbidding a CLI
from reusing another application's `client_id`. The reality on the ground:

- The **entire ecosystem** of unsupported Copilot clients (OpenClaw, pi, the
  dvcrn proxy, jcode, Jarela, llm-github-copilot, copilot-oauth-proxy, and the
  earlier `github-copilot-api.md` in this repo) reuses **Visual Studio Code's
  public Copilot client ID `Iv1.b507a08c87ecfe98`** (or the legacy
  `01ab8ac9400c4e429b23`). This is a *public* value embedded in open-source
  code, and it is chosen precisely because the Copilot backend applies a
  **per-client-id model allowlist** — using VS Code's client ID is the only one
  that reliably mints Copilot tokens usable against the chat API.
  **Sources (all secondary):** dvcrn/copilot-oauth-proxy
  `pkg/copilot/device_flow.go`; OpenClaw docs; pi `github-copilot.ts`;
  jcode `copilot.rs`; Jarela `github-copilot-auth.ts`; llm-github-copilot;
  plus the existing `github-copilot-api.md` research file in this repo.

- **Consequence of using an unauthorized / "wrong" client_id:** token exchange
  may fail or the chat API may reject the request with model-not-supported /
  400 style errors, because the backend keys model entitlements off the client.
  This is the practical (second-hand) reason tools hardcode VS Code's ID
  **Sources:** `github-copilot-api.md` in this repo (secondary);
  dvcrn authentication_flow.md (secondary).

- **Security posture:** GitHub itself frames misuse of the device flow as an
  abuse/phishing vector and forces per-app opt-in partly for this reason (see
  the 2022 changelog). Third-party security research (Praetorian, 2025)
  confirms that attacker-selected `client_id`s, including GitHub-maintained
  ones, display **fewer warnings** to the target user during consent, and that
  GitHub sends a notification email only for apps it considers "third-party"
  (not GitHub-owned). So "misappropriating" a GitHub-owned client_id is
  *possible* and *common* but sits squarely against the abuse posture GitHub
  documents. **Sources (secondary/third-party):** Praetorian
  "GitHub Device Code Phishing" (https://www.praetorian.com/blog/introducing-github-device-code-phishing/);
  DarkAtlas device-code phishing guide;
  GitHub Docs best-practices page.

**`[FLAG]`** There is **no documented GitHub policy** stating "a product must
not use another product's client_id." The consequences of doing so are (a) an
unofficial/unapproved dependency on VS Code's app registration (could be
revoked/rate-limited/changed by GitHub at any time), and (b) potential
account-trust/abuse issues for the user. A **plugin-owned OAuth app with device
flow enabled and its own client_id** is the "clean" path but, for Copilot
specifically, several community authors report they had to use VS Code's
client_id to get the chat API to accept the token — this is the key tension for
a plugin-owned login.

---

## 3. Token shape

### What is issued on completion — access_token and (optionally) refresh_token

By default the device-flow token response is an **access_token** only
(`access_token=gho_...`, `token_type=bearer`, `scope`). No refresh token unless
the app opts in.

**Source:** GitHub Docs, Device flow response (github.com + GHE).

**Refresh token appears when:**
1. The OAuth app / GitHub App is configured for **expiring access tokens**, or
2. The device flow requests the **`offline_access` scope** at runtime.

In either case the response adds `refresh_token`, `expires_in`, and
`refresh_token_expires_in`.

**Source:** GitHub Docs, "Expiring access tokens" + "Opting in to expiring
tokens at runtime" (github.com
https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/authorizing-oauth-apps#expiring-access-tokens).

### Expiry / rotation policy for the GitHub OAuth token

- **Access token:** expires after **8 hours** (`expires_in` = 28800 s).
- **Refresh token:** expires after **6 months without use**
  (`refresh_token_expires_in` = 15897600 s).
- **Rotation:** using a refresh token generates a new access token **and a new
  refresh token**; the old refresh token and old access token immediately stop
  working. Scopes are preserved (cannot be changed on refresh).
- **Refresh endpoint:** `POST /login/oauth/access_token` with
  `grant_type=refresh_token`. For device-flow-issued tokens, **no client_secret
  is required** ("Required unless the token was generated using the device
  flow"). A bad/expired refresh token yields `bad_refresh_token`; the user must
  re-run the flow.

**Source:** GitHub Docs, "Expiring access tokens" + "Refreshing an access token
with a refresh token" (github.com; the GHE page omits the token-refresh
section **`[FLAG]`** — see §4).

### Can refresh tokens mint new access tokens offline? — Yes (GitHub OAuth level)

Yes — that is exactly what a refresh token is for: an offline client without a
browser can call the token endpoint with `grant_type=refresh_token` to mint a
new access token (and new refresh token) without user interaction, up to the
6-month validity. This is the mechanism a plugin could use to keep a GitHub
token alive across sessions.

### GHE nuance: don't assume a refresh token is always present

GitHub explicitly warns that apps supporting both github.com and GHE should
treat the refresh token as optional:

> "If your app supports both GitHub Enterprise Server and GitHub.com, you
> should be prepared for the `offline_access` scope to have no effect, because
> the GitHub Enterprise Server instance may not yet support expiring tokens. In
> this case, you will receive a non-expiring token and no refresh token."

**Source:** GitHub Docs, "Opting in to expiring tokens at runtime"
(https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/authorizing-oauth-apps#opting-in-to-expiring-tokens-at-runtime). **`[FLAG]`** — this says GHE *"may not yet"* support it; per-version availability is not pinned in the docs.

### How the GitHub token feeds Copilot's token exchange

The resulting GitHub OAuth token is **not** what the chat API consumes. It is
exchanged at the (undocumented) internal endpoint:

```
GET https://api.github.com/copilot_internal/v2/token
Authorization: token <github_oauth_token>    # note "token" scheme, not Bearer
Accept: application/json
```

which returns a **short-lived Copilot JWT** (~25–30 minutes, `token` field) plus
`expires_at`, `refresh_in`, and an `endpoints.api` field describing the correct
chat API base per subscription tier (individual / business / enterprise).
There is **no refresh token for the Copilot token**; you re-`GET` the exchange
endpoint with the still-valid GitHub token to mint a fresh one. The community
consensus is to re-exchange at `refresh_in − 60 s` (or a fixed ~5 min buffer).

**Source:** all **secondary** — dvcrn/copilot-oauth-proxy
`pkg/copilot/auth.go` + `authentication_flow.md`; the existing
`github-copilot-api.md` research file in this repo; OpenClaw docs; pi
`github-copilot.ts`; the gh-aw-firewall and hermes issue threads referenced in
§4. **`[FLAG]`**: everything about `/copilot_internal/v2/token` and the Copilot
JWT is undocumented and reverse-engineered; treat as changeable.

**Token type note:** device flow against a GitHub **App** yields a **`ghu_`**
user-to-server token; device flow against an **OAuth App** yields a **`gho_`**
token. Both are accepted by the Copilot exchange (with some extra handling for
`ghu_`), and fine-grained PATs (`github_pat_`) with the "Copilot Requests"
permission are also accepted in many setups. Classic PATs (`ghp_`) are **not**
supported by the Copilot CLI/SDK.
**Sources (mixed):** GitHub Docs "Generating a user access token for a GitHub
App" (primary) for `ghu_`/device-flow; GitHub Docs Copilot SDK auth page and the
Copilot CLI "Authenticate" page (primary for token-prefix table
`gho_`/`github_pat_`/`ghu_` supported, `ghp_` not);
gh-aw-firewall issue #1330 (secondary) for PAT-vs-device-flow behavior on GHE.

---

## 4. GitHub Enterprise (GHE) differences

### Device flow is supported on GHE

GitHub Enterprise Server documents the device flow from at least v2.22 onward
(older docs mark it "public beta and subject to change"). The endpoint shape is
identical to github.com but with the instance hostname substituted:

```
POST http(s)://HOSTNAME/login/device/code
POST http(s)://HOSTNAME/login/oauth/access_token
User visits: http(s)://HOSTNAME/login/device
```

**Source:** GitHub Docs, GitHub Enterprise Server 3.21 and 3.20 "Authorizing
OAuth apps" (primary); older Enterprise 3.1/3.7/3.18 docs (primary).

**Login host vs API host.** In GHE the **OAuth/device endpoints live on the
login host** (`HOSTNAME/login/...`), whereas API calls go to
`api.HOSTNAME` (or `HOSTNAME/api/v3` for older GHE). Device flow only ever uses
the login host — there is no separate "device host."

### Enterprise Copilot token exchange

For GHE the exchange endpoint is on the **API host**, interchanging the domain:

```
GET https://api.<enterprise-domain>/copilot_internal/v2/token
Authorization: token <github_oauth_token>
```

and (in the community integrations) the resulting Copilot endpoint is
`https://copilot-api.<enterprise-domain>` (or `api.<domain>` / the
`endpoints.api` field from the response).

**Sources (all secondary):** the existing `github-copilot-api.md` research file;
OpenClaw docs (`https://<tenant>/login/device/code`,
`https://api.<tenant>/copilot_internal/v2/token`,
`https://copilot-api.<tenant>`); pi `github-copilot.ts`; the VS Code
agent-host issue #313396 ("mint URL must point to `api.<enterprise-host>/copilot_internal/v2/token` instead"); hermes-agent issue #11442 (GHE server,
exchange at `https://{github_host}/api/v3/copilot_internal/v2/token`); goose PR
#8470 (`https://<company>.ghe.com/api/copilot_internal/v2/token`).
Note the recurring inconsistency across tools in the exact GHE path
(`api.<host>/...` vs `<host>/api/v3/...`) — **`[FLAG]`** — there is no single
documented GHE Copilot exchange path; the safest approach is to read the
`hosts.json`/`apps.json` GHE entry and honour whatever the token-exchange
response's `endpoints.api` returns.

### Enterprise-specific restrictions relevant to Copilot auth

- **A GHE token cannot be exchanged on github.com and vice-versa.** Exchanging
  a GHE (or GHEC data-residency `*.ghe.com`) token against
  `api.github.com/copilot_internal/v2/token` returns **HTTP 401 "Bad
  credentials"** — the originating github.com host must be swapped for the
  tenant's API host.
  **Sources (secondary):** hermes issue #11442; gh-aw-firewall #1315 (Test 3);
  lobe-chat issue #17254.
- **PAT-based exchange on GHE data residency can fail.** On GHEC
  data-residency (`*.ghe.com`) tenants, `/copilot_internal/v2/token` returns
  **HTTP 403 "Resource not accessible by personal access token"** for
  fine-grained PATs, even with the Copilot Requests permission. The official
  **Copilot CLI's own device-flow path works** on these tenants (authenticating
  via `GET /copilot_internal/user` and routing inference to a separate
  `copilot-api.<tenant>` subdomain), which is why community guidance is to use
  the CLI's device-flow mechanism rather than a PAT for GHE.
  **Sources (secondary):** gh-aw-firewall #1330 (with a full HTTP traffic
  capture of the successful CLI flow) and #1315; hermes-agent #96164/#96191.
  **`[FLAG]`**: GHE / GHEC device-flow→Copilot behavior is only attested
  second-hand; there is no official doc pinning these exact endpoints or the
  `/copilot_internal/user` auth check.

### What other Copilot CLI integrations have done for GHE auth

- **OpenClaw**: exposes an explicit "GitHub Copilot (Enterprise / data
  residency)" auth method; you supply the tenant root (`your-org.ghe.com`), and
  it derives device/OAuth/Copilot endpoints from it, persists the tenant in
  config, scopes each minted token to its domain, and forces a fresh login when
  switching domains. Strips `*.ghe.com` derivation from the tenant root.
- **VS Code agent host**: added a configurable GHE host; device flow runs
  against `HOSTNAME/login/oauth`, and the Copilot mint URL becomes
  `api.<HOST>/copilot_internal/v2/token`; it also prefers
  `copilotToken.endpoints.api` for the effective chat API URL.
- **hermes / goose / lobe-chat / pi**: each added a `github_host` /
  `COPILOT_GH_HOST`-style config and derived `api.<host>` / `copilot-api.<host>`
  endpoints, precisely because a hardcoded `api.github.com` exchange fails with
  401/403/406 for GHE and GHEC tenants.

**Sources:** OpenClaw docs; microsoft/vscode#313396 + PR #314724; hermes
#11442/#96164/#96191; goose #8470; lobe-chat #17254; pi `github-copilot.ts`.
All secondary. The recurring, robust pattern: **make the GitHub host a
configurable input, derive OAuth endpoints from it, and honour the Copilot
`endpoints.api` returned by the exchange.**

---

## 5. Security / UX

### Screen-scraping / verifying the verification URL

- The device flow has **no localhost callback and no redirect** — it is a
  server-issued user code entered at a browser page
  (`https://github.com/login/device` or the GHE equivalent). This is a UX
  advantage for a CLI (no open port, no redirect registered).
- **Security guidance / best practice:** because the user_code–verification
  channel is unauthenticated, the primary defense is teaching the user to
  verify the destination URL and the requesting app. GitHub's own framing is
  that the flow "does not require redirect URIs at all, which means that an
  attacker can use the device flow to remotely impersonate your app as part of
  a phishing attack" — hence the advice to only enable device flow for
  constrained environments (CLIs, IoT, headless), and to be cautious about
  gating your own services on tokens minted by a public client (public
  clients are trivially spoofable; anyone can reuse your client_id).
  **Source:** GitHub Docs "Best practices for creating a GitHub App"; DarkAtlas
  and Praetorian device-code phishing write-ups (secondary/third-party).
- Community implementations parse the `verification_uri` with a URL-validator
  before passing it to `xdg-open`/`open` to avoid shell/argument injection, and
  force it to be a well-formed URL. The pi integration is an explicit example of
  this defensive pattern. **Source:** pi `github-copilot.ts` (secondary).
- **`[FLAG]`** — GitHub provides no official "verify this URL is the device
  login page" API or recommendation section beyond generic OAuth phishing
  guidance. "Screen scraping guidance" per se is not documented; the actionable,
  documented fact is the RRFC 8628/10027 cross-device phishing concern and the
  2022 opt-in change.

### Code expiry times

- `user_code` / `device_code` expire after `expires_in` (default **900 s =
  15 min**). Must restart the flow on `expired_token` / `token_expired`.
- The web-flow authorization `code` (unrelated) expires after 10 minutes.
- GitHub OAuth **access** token: 8 hours. **Refresh** token: 6 months without
  use (when expiring tokens / `offline_access` are in use).
- Copilot JWT: ~25–30 minutes; re-mint by re-exchanging the GitHub token.

**Sources:** GitHub Docs Device flow (github.com + GHE) for 15 min / 10 min;
"Expiring access tokens" for 8 h / 6 months; community docs for the Copilot JWT
(secondary).

### Polling-interval etiquette

- Poll at **exactly `interval`** (default 5 s), never faster, or GitHub returns
  `slow_down` and **adds 5 s to the interval** (error body includes the new
  interval value); comply with the raised interval thereafter.
- Do not poll past `expires_in` (15 min); abort and request a new device code.
- Respect the app-level browser-verification rate limit (50 submissions/hour).
- Community implementations typically sleep `interval` between polls and set a
  hard deadline at `expires_in`. The `slow_down`-response handling (bumping the
  interval) is the correct etiquette and is explicitly what GitHub documents.

**Sources:** GitHub Docs "Rate limits for the device flow" + "Error codes"
(github.com + GHE) for the primary facts; dvcrn device_flow.go and the
various poll loops (secondary) as corroborating implementation examples.

---

## Feasibility notes for a plugin-owned device-flow login

These are the facts most likely to change the design, gathered from the
sections above:

1. **A plugin can technically own the whole flow** — it needs its own (or a
   shared/common) `client_id` from an app that has device flow enabled, then it
   can request a device code, show URL+code, poll, and store the token. The GHE
   variant is the same flow against `HOSTNAME`. No callback URL, no secret, no
   open port are involved.
2. **The clean client_id path conflicts with Copilot's unofficial allowlist.**
   The chat/token-exchange backend keys model entitlement off the client_id;
   the ecosystem converges on VS Code's `Iv1.b507a08c87ecfe98`. A brand-new
   plugin-owned app client_id may pass OAuth but yield Copilot tokens that the
   chat API rejects. This is the central design risk and is **`[FLAG]`/
   undocumented** — it must be validated empirically before committing to a
   plugin-owned OAuth app.
3. **Token lifetime strategy:** a device-flow GitHub token (with expiring
   tokens / `offline_access`) yields an 8 h token + 6-month refresh token that
   can be rotated offline, so a plugin could go months without re-prompting on
   github.com. On GHE, refresh tokens may not exist (per GitHub's own warning),
   so the plugin must not assume one; a non-expiring GHE token is the
   anticipated fallback.
4. **GHE requires host-aware routing.** A single `api.github.com` exchange
   fails for GHE/GHEC and the wrong token can't cross hosts. The plugin must
   derive the device/OAuth/exchange/chat endpoints from the configured GitHub
   host (the adapters in §4 are the model), and prefer the token-exchange
   response's `endpoints.api` for the chat base URL.
5. **Security:** since device-flow codes are a known phishing surface, the
   plugin should validate the verification URL, print it helpfully, default to a
   `read:user` scope, avoid broad scopes, and store the token with owner-only
   permissions; be prepared for GitHub to treat reuse of a GitHub-owned
   client_id as abuse.

---

## Sources list

### Primary (GitHub documentation)

- Authorizing OAuth apps — github.com, Device flow + Error codes + Expiring
  tokens + Refreshing: https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/authorizing-oauth-apps
- Authorizing OAuth apps — GitHub Enterprise Server 3.21 (device flow on GHE):
  https://docs.github.com/en/enterprise-server@3.21/apps/oauth-apps/building-oauth-apps/authorizing-oauth-apps
- Authorizing OAuth apps — GHE 3.20 / 3.18 / 3.7 / 3.1 (device flow on GHE,
  "public beta"):
  https://docs.github.com/en/enterprise-server@3.20/apps/oauth-apps/building-oauth-apps/authorizing-oauth-apps
- Creating an OAuth app ("Enable Device Flow" checkbox):
  https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/creating-an-oauth-app
- Generating a user access token for a GitHub App (device flow for GitHub Apps,
  `ghu_`): https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-a-user-access-token-for-a-github-app
- Building a CLI with a GitHub App (device-flow tutorial):
  https://docs.github.com/en/apps/creating-github-apps/writing-code-for-a-github-app/building-a-cli-with-a-github-app
- Best practices for creating a GitHub App (device flow phishing; "does not
  require redirect URIs at all"; public clients trivially spoofable):
  https://docs.github.com/en/apps/creating-github-apps/about-creating-github-apps/best-practices-for-creating-a-github-app
- GitHub Changelog — Enable OAuth Device Authentication Flow for Apps (2022-03-16):
  https://github.blog/changelog/2022-03-16-enable-oauth-device-authentication-flow-for-apps/
- Copilot CLI authentication (Supported token types table; env-var precedence;
  GHE data-residency login):
  https://docs.github.com/en/copilot/how-tos/copilot-cli/set-up-copilot-cli/authenticate-copilot-cli
- Copilot SDK Auth (token types `gho_`/`ghu_`/`github_pat_`; `ghp_` not
  supported; device flow default):
  https://docs.github.com/en/copilot/how-tos/copilot-sdk/auth/authenticate
  (repo: https://github.com/github/copilot-sdk/blob/main/docs/auth/authenticate.md)

### Secondary (real integrations / research)

- dvcrn/copilot-oauth-proxy — device flow source `pkg/copilot/device_flow.go`,
  token exchange `pkg/copilot/auth.go`, `authentication_flow.md`:
  https://github.com/dvcrn/copilot-oauth-proxy
- Existing repo research file (device-flow→Copilot exchange, model allowlist,
  GHE URLs): https://github.com/anomalyco/opencode/issues/20759
  (referenced from `docs/research/github-copilot-api.md`)
- OpenClaw GitHub Copilot provider docs (GHE data-residency device flow,
  derived endpoints, per-domain token scoping):
  https://docs.openclaw.ai/providers/github-copilot
- pi `packages/ai/src/auth/oauth/github-copilot.ts` (device flow + GHE domain
  derivation + URL validation): https://github.com/earendil-works/pi/blob/209bc7b9/packages/ai/src/auth/oauth/github-copilot.ts
- jcode `crates/jcode-base/src/auth/copilot.rs` (client_id, hosts.json/apps.json
  token sources, save flow): https://github.com/1jehuang/jcode/blob/a63dbc45/crates/jcode-base/src/auth/copilot.rs
- Jarela `lib/providers/github-copilot-auth.ts`; llm-github-copilot
  `llm_github_copilot.py`; alialfredji/copilot-llm (client_id reuse):
  https://github.com/CircuitWall/jarela/blob/master/lib/providers/github-copilot-auth.ts ,
  https://github.com/jmdaly/llm-github-copilot/blob/c5735f4d15baf85228c191929f73582f5dc1d5e6/llm_github_copilot.py ,
  https://github.com/alialfredji/copilot-auth
- microsoft/vscode#313396 + PR #314724 (GHE host for Copilot auth; mint URL
  `api.<host>/copilot_internal/v2/token`; prefer `endpoints.api`):
  https://github.com/microsoft/vscode/issues/313396 ,
  https://github.com/microsoft/vscode/pull/314724
- github/gh-aw-firewall#1315 & #1330 (GHE/GHEC token exchange 401/403, PAT vs
  device flow, full traffic capture of CLI GHE auth incl. `/copilot_internal/user`
  and `copilot-api.<tenant>`):
  https://github.com/github/gh-aw-firewall/issues/1315 ,
  https://github.com/github/gh-aw-firewall/issues/1330
- hermes-agent issues/PRs #11442, #96164/#96191, #78378 (GHE exchange endpoint,
  `api.<host>` vs `<host>/api/v3`, 401/403/406): https://github.com/NousResearch/hermes-agent
- aaif-goose/goose PR #8470 / #8494 (GHE token endpoint `/<company>.ghe.com/api/copilot_internal/v2/token`): https://github.com/aaif-goose/goose/pull/8470
- lobe-chat issue #17254 (data-residency host table for device/OAuth/exchange/
  user/chat endpoints): https://github.com/lobehub/lobe-chat/issues/17254
- Security research (secondary, for §2/§5): Praetorian "GitHub Device Code
  Phishing" (https://www.praetorian.com/blog/introducing-github-device-code-phishing/);
  DarkAtlas device-code phishing guide
  (https://darkatlas.io/blog/the-code-is-real-the-device-is-not-the-definitive-guide-to-device-code-phishing);
  AppOmni device-code-phishing write-up.
- RFC 8628 (OAuth 2.0 Device Authorization Grant) and RFC 10027 (Best Current
  Practice for Security of Cross-Device Flows) — referenced by GitHub's docs and
  the phishing literature:
  https://datatracker.ietf.org/doc/html/rfc8628 ,
  https://datatracker.ietf.org/doc/html/rfc10027
