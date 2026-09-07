# OAuth App Registration & Device Flow on GHEC Data Residency (`*.ghe.com`) — Research for Plugin-Owned Auth

Research compiled for the genie Copilot wire plugin
(`github.com/okayest-dev/genie-plugin-wire-copilot`). Goal: pin down, from
**primary sources**, whether an OAuth App / GitHub App can be **registered
inside** a GitHub Enterprise Cloud (GHEC) data-residency tenant (custom
`<tenant>.ghe.com` domain), whether that tenant exposes its own **developer
settings / OAuth-app registration UI**, which device-flow endpoints it uses,
and how the plugin should get a `client_id` usable against that tenant's device
flow. This feeds a decision on whether to register the OAuth app on
**github.com**, on **the `.ghe.com` tenant**, or **both**.

**Scope / prior art.** This file is a follow-on to
`docs/research/copilot-device-flow-oauth.md`, which established the device flow
mechanics for github.com and GitHub Enterprise Server (GHE). Here we focus
exclusively on **GHEC with data residency** (`*.ghe.com`). The underlying
premise ("plugin-owned token may fail Copilot's client-id allowlist") is not
re-argued; it is carried forward and re-cited only where it interacts with the
data-residency decision.

Every claim carries an inline source. Primary = official GitHub docs. Any
claim sourced from a third-party vendor doc or issue tracker is **flagged as
secondary**. Anything ambiguous / undocumented / not directly observable for
the private `kudelski.ghe.com` tenant is flagged **`[FLAG]`**.

---

## 1. Can an app be registered *inside* a GHEC data-residency tenant? — Yes

### 1.1 The tenant is a self-contained, isolated GitHub host with its own developer settings

The authoritative overview states:

> "Your enterprise will be hosted on a dedicated subdomain of GHE.com." …
> "managed user accounts access your resources through a dedicated subdomain
> of GHE.com, and can only interact with resources that belong to your
> enterprise."

and, under "Developer experience":

> "The developer experience on GHE.com differs in some ways from GitHub.com
> and GitHub Enterprise Server."

**Source (primary):** GitHub Docs, "About GitHub Enterprise Cloud with data residency"
(https://docs.github.com/en/enterprise-cloud@latest/admin/data-residency/about-github-enterprise-cloud-with-data-residency).

The GHE.com network page confirms the client-facing host surface includes the
tenant's own subdomains:

> `*.SUBDOMAIN.ghe.com` — where SUBDOMAIN is your enterprise's dedicated
> subdomain on GHE.com

**Source (primary):** GitHub Docs, "Network details for GHE.com"
(https://docs.github.com/en/enterprise-cloud@latest/admin/data-residency/network-details-for-ghecom).

### 1.2 GitHub Apps can be registered on the tenant via its own developer-settings UI

The "About creating GitHub Apps" page (which is the Enterprise Cloud edition
and carries the "enterprise-owned GitHub App" concept) describes creating apps
under an enterprise, org, or personal account. Its companion "Registering a
GitHub App" page (Enterprise Cloud edition) describes navigating the tenant's
own **Developer settings → GitHub Apps** UI, and for an enterprise-owned app:
"under 'Settings', click GitHub Apps" (i.e. the enterprise settings sidebar on
the tenant).

**Source (primary):** GitHub Docs,
- "About creating GitHub Apps" (https://docs.github.com/en/enterprise-cloud@latest/apps/creating-github-apps/about-creating-github-apps/about-creating-github-apps)
- "Registering a GitHub App" (https://docs.github.com/en/enterprise-cloud@latest/apps/creating-github-apps/registering-a-github-app/registering-a-github-app)
- "Modifying a GitHub App registration" (https://docs.github.com/en/enterprise-cloud@latest/apps/maintaining-github-apps/modifying-a-github-app-registration)

### 1.3 OAuth Apps can be created on the tenant too (third-party integrations do it)

The official "Creating an OAuth app" page is served in the Enterprise Cloud
edition and describes the standard "Developer settings → OAuth apps → New
OAuth App" flow, though its examples use `github.com`.

**Source (primary):** GitHub Docs, "Creating an OAuth app"
(https://docs.github.com/en/enterprise-cloud@latest/apps/oauth-apps/building-oauth-apps/creating-an-oauth-app)
("If your OAuth app will use the device flow … click **Enable Device Flow**").

That GitHub's own Enterprise Cloud edition ships the full apps section implies
the same UI surface exists on `.ghe.com` tenants (GHE.com runs the same
developer platform under a custom domain). Direct, non-GitHub confirmation that
OAuth App registration works **inside** `.ghe.com` tenants comes from several
independent SaaS vendors whose documented setup is "create an OAuth App on
your `<tenant>.ghe.com` tenant":

- **Apidog** (secondary): "First, create an OAuth App on your GitHub
  Enterprise Cloud tenant. … sign in to your … `.ghe.com` site … Go to the
  OAuth Apps settings page. Create a new OAuth App. … Copy the Client ID.
  Generate and copy the Client Secret." It requires the caller to have
  "permission to create an OAuth App on that tenant."
  (https://apidog.com/blog/github-enterprise-cloud-data-residency-apidog/ ,
  https://docs.apidog.com/github-enterprise-cloud-2305226m0)

- **CodeRabbit** (secondary): "Access to create OAuth Apps and GitHub Apps in
  your organization … navigate to your GitHub Enterprise Server instance and
  create an OAuth App." It explicitly says for `*.ghe.com` hosts the creating
  principal is an **enterprise owner** (whereas classic GHE uses a
  site_admin), and that `*.ghe.com` hosts "do not expose the GitHub Enterprise
  Server `site_admin` role." (https://docs.coderabbit.ai/platforms/github-enterprise-server)

- **Microsoft Azure SRE Agent** (secondary): states `.ghe.com` hosts expose
  `Settings > Developer settings > GitHub Apps > New GitHub App` on the GHE
  tenant, and that for `.ghe.com` **OAuth and PAT are not available** — only a
  GitHub App (BYO App). (https://github.com/MicrosoftDocs/azure-docs/blob/main/articles/sre-agent/connect-github-enterprise-cloud.md)

  **`[FLAG]`**: The Azure statement "OAuth and PAT aren't available for GHE
  hosts" refers to that *specific Azure connector's auth method*, not a
  universal ban on using OAuth Apps on `.ghe.com`. Apidog and CodeRabbit
  register OAuth Apps *on* the tenant, so a blanket "no OAuth Apps on
  `.ghe.com`" is clearly false. The nuance is that Enterprise Managed Users on
  `.ghe.com` are provisioned/authenticated via SAML/OIDC and cannot log into
  `.github.com`, so a github.com-registered OAuth app generally cannot drive a
  `.ghe.com` session. See §3.

### 1.4 The core isolation fact: apps registered on github.com do NOT cross into a `.ghe.com` tenant

The GitHub Community answer (secondary, but from a knowledgeable ecosystem
user; matches the isolation model in the primary docs):

> "Short answer: no, not directly. The app registered on github.com can't be
> installed on a ghe.com tenant. Enterprise Cloud with data residency (ghe.com)
> runs as a fully isolated tenant from github.com. Different host, different
> user identity backing, different Marketplace. Apps registered on github.com
> live on that host and don't cross over. To make your app installable on
> `enterprise-test-eu1.ghe.com`, you need to register it again inside that
> tenant."

**Source (secondary):** github.com/orgs/community/discussions/193164
("Github App that is in https://github.com can it be installed in ghe.com
Account", Apr 2026).

This is corroborated by the primary docs' isolation model (managed users "can
only interact with resources that belong to your enterprise", data stays in
the region, different URL surface).

### 1.5 Bottom line for Q1

- **Yes, apps can be registered *inside* a `.ghe.com` tenant** — the tenant
  exposes its own Developer settings / GitHub Apps / OAuth Apps UI, and both
  first-party docs and several vendor integrations rely on tenant-local app
  registration.
- **An app registered on github.com does not work against a `.ghe.com`
  tenant** (and vice-versa). So for a plugin that must authenticate a
  `kudelski.ghe.com` user, the plugin's OAuth app **must be registered on the
  tenant itself** to get a tenant-local `client_id`.
- **`[FLAG]` — OAuth App (vs GitHub App) availability on `.ghe.com` for
  device-flow tokens is the open question.** GitHub's own Enterprise Cloud
  docs describe generic OAuth-app creation, but do *not* call out
  `.ghe.com`-specific OAuth-App limits. Third-party evidence is split: Apidog
  + CodeRabbit register OAuth Apps on the tenant; Azure's connector claims
  "OAuth … not available" for `.ghe.com` (see flag in §1.3). The Azure claim
  most plausibly means OAuth tokens minted on github.com don't authenticate a
  `.ghe.com` session, not that tenant-local OAuth Apps are impossible — but
  this is **`[FLAG]`** and, if OAuth App device flow is broken on the tenant,
  a plugin-owned **GitHub App** (device flow → `ghu_` token) is the viable
  tenant-local alternative.

---

## 2. Which endpoints does GHEC data-residency device flow use? — `HOSTNAME/login/*` on the tenant domain

The Enterprise Cloud "Authorizing OAuth apps" page carries the explicit
qualifier:

> "[!NOTE] This article contains commands or examples that use the
> `github.com` domain. You might access GitHub at a different domain, such as
> `octocorp.ghe.com`."

The device-flow endpoints on that page are given against `github.com`:

```
POST https://github.com/login/device/code
POST https://github.com/login/oauth/access_token
User visits https://github.com/login/device
```

**Source (primary):** GitHub Docs, "Authorizing OAuth apps" — Enterprise Cloud
edition (https://docs.github.com/en/enterprise-cloud@latest/apps/oauth-apps/building-oauth-apps/authorizing-oauth-apps). The same qualifier and `github.com`
examples appear throughout the GitHub App device-flow pages in the Enterprise
Cloud edition.

Applying the docs' own substitution rule ("you might access GitHub at a
different domain, such as `octocorp.ghe.com`"), for a `kudelski.ghe.com`
tenant the endpoints become:

```
POST https://kudelski.ghe.com/login/device/code
POST https://kudelski.ghe.com/login/oauth/access_token
User visits https://kudelski.ghe.com/login/device
```

The class-level GHE behavior (device/oauth on the **login host** `HOSTNAME`,
API on `api.HOSTNAME`) documented in `copilot-device-flow-oauth.md` §4 carries
over: GHEC data-residency tenants serve the device/OAuth flow on the tenant
root `<tenant>.ghe.com`, not on `api.<tenant>.ghe.com`.

**Corroboration (all secondary), showing the same `HOSTNAME/login/device/code`
shape on `.ghe.com` tenants in working integrations:**

- **LobeHub issue** mapping for a `companyname.ghe.com` tenant:
  `https://companyname.ghe.com/login/device/code`,
  `https://companyname.ghe.com/login/oauth/access_token`,
  `https://api.companyname.ghe.com/copilot_internal/v2/token`,
  `https://api.companyname.ghe.com/user` (https://github.com/lobehub/lobe-chat/issues/17254)
- **OpenClaw** (secondary): device flow resolves to
  `https://your-org.ghe.com/login/device/code`,
  token exchange `https://api.your-org.ghe.com/copilot_internal/v2/token`,
  completions `https://copilot-api.your-org.ghe.com`
  (https://docs.openclaw.ai/providers/github-copilot)
- **Copilot CLI** (primary, commands) explicitly uses `--host SUBDOMAIN.ghe.com`
  for the data-residency device login and shows the prompt "GitHub Enterprise
  Cloud with data residency (*.ghe.com)" (https://docs.github.com/en/copilot/how-tos/copilot-cli/set-up-copilot-cli/authenticate-copilot-cli;
  and https://docs.github.com/en/enterprise-cloud@latest/copilot/how-tos/configure-personal-settings/authenticate-to-ghecom).

**Bottom line for Q2:** Yes, device flow on a `.ghe.com` tenant uses the same
`HOSTNAME/login/device/code` + `HOSTNAME/login/oauth/access_token` +
`HOSTNAME/login/device` endpoints, with **`HOSTNAME` = `<tenant>.ghe.com`**
(the tenant root domain), *not* the `api.` prefix nor github.com.

---

## 3. Copilot login on `.ghe.com`: tenant API host, and the client_id story

### 3.1 The token exchange goes to the tenant's API host

For the Copilot token exchange, the endpoint is
`https://api.<tenant>.ghe.com/copilot_internal/v2/token` (API host, not login
host). This is the pattern in every secondary integration and matches the
`/meta` host surface noted in §1.1 (the tenant and its `*.` subdomains).

**Sources:** LobeHub #17254; OpenClaw docs; github-mcp-server discussion #2926
(all secondary; the github-mcp-server discussion describes the `newGHECHost`
→ `https://api.<host>` routing and the `copilot-api.SUBDOMAIN.ghe.com`
credential/MCP authorization-server endpoints).

The Copilot allowlist reference (primary) confirms GHE.com Copilot traffic is
served from the tenant's own subdomains and that individual github.com Copilot
domains are **not** required on GHE.com:

> "All other domains that are required on GitHub.com are not required on
> GHE.com. For example: Individual services have a dedicated endpoint on your
> subdomain (such as `https://copilot-proxy.SUBDOMAIN.ghe.com/`)"

**Source (primary):** GitHub Docs, "Copilot allowlist reference"
(https://docs.github.com/en/enterprise-cloud@latest/copilot/reference/copilot-allowlist-reference#copilot-on-ghecom).

### 3.2 Is there a documented device-flow path with no pre-registered tenant app client_id? — No official one; the "no-app" path is GitHub's own apps only

**Q3 asks:** is there a device-flow path on `.ghe.com` that works without a
pre-registered app client_id (e.g. GitHub's own Copilot CLI client_id)?

Findings:

- **GitHub's official docs do not document a client_id-free device flow.** The
  device flow always requires a registered app's `client_id`
  (`incorrect_client_credentials` if missing), per the Enterprise Cloud
  "Authorizing OAuth apps" page (§2 source). There is no officially documented
  "anonymous" or "default GitHub client_id" for the device flow.

- **The official Copilot CLI works on `.ghe.com` with *its own* client_id.** The
  Copilot CLI "Authenticating" page (primary) documents `gho_` (OAuth device
  flow) as the default and supported auth for `*.ghe.com` via
  `copilot login --host SUBDOMAIN.ghe.com`. The CLI's OAuth token comes from
  **the Copilot CLI's own registered app** ("OAuth tokens from the GitHub
  Copilot CLI app", per the CLI command reference), running its device flow
  against the tenant's `HOSTNAME/login/*`. So the only "already works without
  you registering an app" path on `.ghe.com` is **GitHub's own apps** (Copilot
  CLI, `gh`, VS Code), whose private/opaque client_ids the plugin cannot
  legitimately reuse as its own.

  **Sources (primary):**
  - Copilot CLI auth page (https://docs.github.com/en/copilot/how-tos/copilot-cli/set-up-copilot-cli/authenticate-copilot-cli)
  - Copilot CLI command reference (raw
    https://raw.githubusercontent.com/github/docs/main/content/copilot/reference/copilot-cli-reference/cli-command-reference.md) — "OAuth tokens from the GitHub Copilot CLI app", `--host` for `*.ghe.com`, device-code default on remote/CI.
  - GHEC Copilot auth page (https://docs.github.com/en/enterprise-cloud@latest/copilot/how-tos/configure-personal-settings/authenticate-to-ghecom) — `copilot login --host SUBDOMAIN.ghe.com`.

- **Reusing GitHub's own client_id (VS Code's `Iv1.b507a08c87ecfe98`) — the
  ecosystem norm — is exactly the "not plugin-owned" path.** Per
  `copilot-device-flow-oauth.md` §2/feasibility, the whole unsupported-Copilot
  ecosystem reuses VS Code's client_id because the Copilot chat backend keys
  model allowlists off the client_id. That analysis stands; for `.ghe.com` the
  secondary integrations (LobeHub, OpenClaw, etc.) likewise reuse that client_id
  but point the device/OAuth/exchange hosts at the tenant. So a plugin could
  mirror them (tenant hosts + VS Code's client_id), but that is neither
  plugin-owned nor sanctioned; it is the `[FLAG]` risk discussed in the prior
  file.

- **`[FLAG]` — Whether a "clean", tenant-registered, plugin-owned OAuth app
  client_id mints Copilot chat tokens that the tenant accepts is still
  undocumented and must be validated empirically.** This is the same client-id
  allowlist concern from the prior research, now scoped to a `.ghe.com` tenant.
  The prior note that "GHE device-flow→Copilot behavior is only attested
  second-hand" applies with added force for data-residency tenants. Because
  the customer (kudelski) is a **private tenant**, we cannot probe it here;
  validation must be done against the live tenant.

### 3.3 PAT-path on `.ghe.com` is unreliable — reinforces device flow (with an app client_id)

Carried from `copilot-device-flow-oauth.md` §4: on GHEC data-residency,
`/copilot_internal/v2/token` returns **403** for fine-grained PATs, while the
official Copilot CLI device-flow path works (via a `/copilot_internal/user`
check and a separate `copilot-api.<tenant>` subdomain). That is second-hand
(gh-aw-firewall #1330/#1315, hermes #96164/#96191). It means the plugin cannot
rely on PAT exchange for `.ghe.com`; a device-flow minted token (which requires
a registered app client_id) is the credible path.

**`[FLAG]`** as before: those `/copilot_internal/*` endpoints and the exact
data-residency transfer are undocumented and could change.

---

## 4. Facts on `kudelski.ghe.com` specifically

`kudelski.ghe.com` is a private GHEC data-residency tenant; nothing about its
internal device-flow / app-registration configuration is publicly observable.

- General mechanism (from §1–§3): the tenant is isolated, exposes its own
  developer-settings UI, serves device flow at `kudelski.ghe.com/login/*`, and
  serves Copilot at `api.kudelski.ghe.com` (`copilot_internal/v2/token`) with
  chat at `copilot-api.kudelski.ghe.com` (or whatever `endpoints.api` in the
  token-exchange response returns).
- **`[FLAG]`** — whether the Kudelski **enterprise admin allows** app
  registration, enables device flow, or restricts third-party apps is a per-tenant
  policy we cannot determine from public sources; must be confirmed inside the
  tenant (e.g. whether `Settings → Developer settings → OAuth apps` / `GitHub
  Apps` appears, whether enterprise policy blocks app installation, and whether
  a plugin-owned app's client_id mints a usable Copilot token).
- We found **no public mention of `kudelski.ghe.com`** in GitHub community /
  issue-tracker searches for OAuth app + device flow + data residency. No
  evidence of earliest-adopter or blocked status either way.

---

## Decision-oriented summary (for "register on github.com, on the tenant, or both")

The evidence points to a clear shape for the plugin's auth design:

1. **A `.ghe.com` session cannot be driven by a github.com-registered app.**
   Managed users are isolated to the tenant; apps registered on github.com do
   not cross over (primary isolation model in §1.4; secondary community
   confirmation). So a github.com-registered OAuth app is **not sufficient**
   for a `kudelski.ghe.com` user. The app must be registered **on the tenant**
   to obtain a tenant-local `client_id`.

2. **Tenant-registered app is the supported mechanism.** The tenant exposes
   its own Developer settings; multiple vendors register OAuth Apps / GitHub
   Apps inside `.ghe.com` tenants and run device flow against
   `<tenant>.ghe.com/login/*` (§1.3, §2).

3. **The open, must-test question is the Copilot client-id allowlist on the
   tenant** (§3.2). A plugin-owned app client_id may authenticate fine (device
   flow + API) but yield Copilot chat tokens the tenant's chat backend rejects,
   because Copilot keys model entitlements off client_id — exactly the tension
   from `copilot-device-flow-oauth.md`. This must be validated **on the live
   `kudelski.ghe.com` tenant** before committing to plugin-owned.

4. **Fallback options if plugin-owned client_id is rejected by the tenant's
   chat backend:** (a) mirror the ecosystem and use VS Code's public client_id
   (`Iv1.b507a08c87ecfe98`) against tenant hosts — works but is not
   plugin-owned and is `[FLAG]` abuse/revocation risk; (b) register on the
   tenant **and** on github.com and route each account/domain to the matching
   app (host-aware), so both `.ghe.com` and regular `github.com` Copilot users
   are covered.

5. **Host-awareness is mandatory.** The device/OAuth hosts are the tenant root
   (`kudelski.ghe.com/login/*`); the Copilot exchange host is
   `api.kudelski.ghe.com`; the chat host is whatever the exchange's
   `endpoints.api` returns (`copilot-api.kudelski.ghe.com` typically). The
   plugin must derive these from the configured tenant, never hardcode
   `github.com`.

---

## Sources list

### Primary (GitHub documentation)

- About GitHub Enterprise Cloud with data residency —
  https://docs.github.com/en/enterprise-cloud@latest/admin/data-residency/about-github-enterprise-cloud-with-data-residency
- Feature overview for GitHub Enterprise Cloud with data residency (URL/API differences) —
  https://docs.github.com/en/enterprise-cloud@latest/admin/data-residency/feature-overview-for-github-enterprise-cloud-with-data-residency
- Network details for GHE.com (`.SUBDOMAIN.ghe.com`, `/meta`, host surface) —
  https://docs.github.com/en/enterprise-cloud@latest/admin/data-residency/network-details-for-ghecom
- About creating GitHub Apps —
  https://docs.github.com/en/enterprise-cloud@latest/apps/creating-github-apps/about-creating-github-apps/about-creating-github-apps
- Registering a GitHub App —
  https://docs.github.com/en/enterprise-cloud@latest/apps/creating-github-apps/registering-a-github-app/registering-a-github-app
- Modifying a GitHub App registration —
  https://docs.github.com/en/enterprise-cloud@latest/apps/maintaining-github-apps/modifying-a-github-app-registration
- Creating an OAuth app (Enable Device Flow checkbox) —
  https://docs.github.com/en/enterprise-cloud@latest/apps/oauth-apps/building-oauth-apps/creating-an-oauth-app
- Modifying an OAuth app —
  https://docs.github.com/en/enterprise-cloud@latest/apps/oauth-apps/maintaining-oauth-apps/modifying-an-oauth-app
- Authorizing OAuth apps — Enterprise Cloud edition (the "you might access
  GitHub at a different domain, such as octocorp.ghe.com" qualifier + device
  flow endpoints) —
  https://docs.github.com/en/enterprise-cloud@latest/apps/oauth-apps/building-oauth-apps/authorizing-oauth-apps
- Generating a user access token for a GitHub App (device flow, `ghu_`) —
  https://docs.github.com/en/enterprise-cloud@latest/apps/creating-github-apps/authenticating-with-a-github-app/generating-a-user-access-token-for-a-github-app
- Building a CLI with a GitHub App (device-flow tutorial; github.com examples +
  "you might access GitHub at a different domain" note) —
  https://docs.github.com/en/enterprise-cloud@latest/apps/creating-github-apps/writing-code-for-a-github-app/building-a-cli-with-a-github-app
- Authenticating GitHub Copilot CLI (`*.ghe.com` login, `--host`, `gho_`) —
  https://docs.github.com/en/copilot/how-tos/copilot-cli/set-up-copilot-cli/authenticate-copilot-cli
- Copilot CLI command reference (raw; "OAuth tokens from the GitHub Copilot CLI
  app", `--host https://example.ghe.com`, device-code default on remote/CI) —
  https://raw.githubusercontent.com/github/docs/main/content/copilot/reference/copilot-cli-reference/cli-command-reference.md
- Using GitHub Copilot with an account on GHE.com (`copilot login --host SUBDOMAIN.ghe.com`) —
  https://docs.github.com/en/enterprise-cloud@latest/copilot/how-tos/configure-personal-settings/authenticate-to-ghecom
- Copilot allowlist reference (GHE.com dedicated subdomain endpoints; github.com
  domains not required on GHE.com) —
  https://docs.github.com/en/enterprise-cloud@latest/copilot/reference/copilot-allowlist-reference

### Secondary (vendor docs / issue trackers / community)

- GitHub Community: "Github App that is in https://github.com can it be
  installed in ghe.com Account. #193164" (isolation; must re-register inside
  tenant) — https://github.com/orgs/community/discussions/193164
- Apidog — "How to Connect a GHE.com Repository to Apidog" (create OAuth App
  on the tenant) — https://apidog.com/blog/github-enterprise-cloud-data-residency-apidog/
  and https://docs.apidog.com/github-enterprise-cloud-2305226m0
- CodeRabbit — GitHub Enterprise Server doc (OAuth/GitHub App on `*.ghe.com`,
  enterprise-owner requirement, no site_admin) —
  https://docs.coderabbit.ai/platforms/github-enterprise-server
- Microsoft Azure SRE Agent doc (BYO GitHub App on `.ghe.com`; "OAuth and PAT
  aren't available for GHE hosts" — flagged) —
  https://github.com/MicrosoftDocs/azure-docs/blob/main/articles/sre-agent/connect-github-enterprise-cloud.md
- SonarQube Cloud — Importing GHE.com Cloud organization (GitHub Apps created
  inside tenant via Developer settings) —
  https://docs.sonarsource.com/sonarqube-cloud/... (referenced, minor)
- LobeHub — "[Request] Support GitHub Copilot on GitHub Enterprise Cloud with
  data residency (*.ghe.com)" #17254 (tenant endpoint mapping table) —
  https://github.com/lobehub/lobe-chat/issues/17254
- OpenClaw — GitHub Copilot provider docs (data-residency device flow,
  derived endpoints, domain scoping) —
  https://docs.openclaw.ai/providers/github-copilot
- github/github-mcp-server Discussion #2926 (GHE.com OAuth/API host routing,
  `newGHECHost` → `https://api.<host>`) —
  https://github.com/github/github-mcp-server/discussions/2926
- github/github-mcp-server Issue #2074 (MCP OAuth on `*.ghe.com` resolves
  authorization server to `copilot-api.SUBDOMAIN.ghe.com`; data-residency
  isolation) — https://github.com/github/github-mcp-server/issues/2074
- Carried from `copilot-device-flow-oauth.md`: gh-aw-firewall #1330/#1315,
  hermes #96164/#96191 (PAT 403 on `.ghe.com`; CLI device flow via
  `/copilot_internal/user` + `copilot-api.<tenant>`) — all secondary; re-flagged.
