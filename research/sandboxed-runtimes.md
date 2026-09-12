# Sandboxed Code-Execution Runtimes for Genie

**Date:** 2026-09-10
**Purpose:** Evaluate runtimes for a `code` tool that replaces `bash` in the genie harness.
The LLM emits short scripts that run in a sandboxed subprocess with granular permissions
(filesystem per-directory, network, subprocess access).

## Requirements

| Requirement | Detail |
|---|---|
| **Permission model** | Granular, independent control over filesystem (per-directory), network, subprocess |
| **Embeddability** | Go harness spawns via `exec.Command`; clean stdout/stderr IPC |
| **Language reach** | At least one language the LLM writes well; ideally polyglot |
| **Availability** | Likely present or easy to install on a developer machine |
| **Maturity** | Stable permission model, not experimental |
| **Structured errors** | Harness can detect permission denial vs runtime error vs user abort |

## Candidates

### 1. Deno (TypeScript/JavaScript Runtime)

**Source:** https://docs.deno.com/runtime/fundamentals/security/ (2026-06-17)

**Permission model:**
- Deno is **sandboxed by default** — zero access to filesystem, network, env, subprocesses unless granted.
- Permissions are independently controllable with `--allow-*` / `--deny-*` flags:
  - `--allow-read[=PATH,...]` / `--allow-write[=PATH,...]` — per-directory granularity. Comma-separated or repeated flags. Can scope to specific dirs.
  - `--allow-net[=HOST:PORT,...]` — per-hostname, per-port granularity.
  - `--allow-run[=PROGRAM,...]` — per-executable subprocess control.
  - `--allow-env[=VAR,...]` — per-variable env access with suffix wildcards.
  - `--allow-ffi[=PATH,...]` / `--allow-sys[=API,...]` / `--allow-import[=HOST,...]`.
- `--deny-*` flags override `--allow-*` (e.g., `--allow-read=/app --deny-read=/app/secrets`).
- **Permission Broker** (`DENO_PERMISSION_BROKER_PATH`): external process can make allow/deny decisions via JSON messages over a Unix domain socket. Fully headless, no prompts.

**Structured errors:**
- Permission denials throw `NotCapable` errors with a structured JSON body: `{ code: "ERR_PERMISSION_DENIED", permission: "read", resource: "/etc/hosts" }`.
- The error class is `Deno.errors.NotCapable` — catchable in JS, and the error code is machine-parseable from the stderr output.
- Deno also supports `DENO_AUDIT_PERMISSIONS` for JSONL audit logs of every permission check.

**Embeddability from Go:**
- Single static binary. No dependencies.
- `deno run --no-prompt --allow-read=/tmp --allow-write=/tmp script.ts` — clean subprocess.
- stdout/stderr separation: stdout for script output, stderr for errors. Perfect IPC.
- `--no-prompt` flag suppresses interactive prompts (required for agent use).

**Language reach:**
- TypeScript/JavaScript natively.
- npm compatibility (can import Node packages).
- WASM support.
- Could theoretically run Python via Pyodide/WASM but that's heavy.

**Availability:**
- Single binary download (curl + unzip). No system deps.
- Available for Linux, macOS, Windows.
- Very popular among developer tools (Deno, Supabase Edge Functions, etc.).
- `brew install deno`, `npm install -g deno`, `curl -fsSL https://deno.land/install.sh | sh`.

**Maturity:**
- Stable permission model since Deno 1.x (2020). Production-hardened.
- Permission Broker added in 2025 with versioned JSON schemas.
- Deno 2.x is GA. Not experimental.

**Rating: ⭐⭐⭐⭐⭐ (Primary Recommendation)**

---

### 2. Node.js with `--experimental-permission`

**Source:** https://nodejs.org/docs/latest-v20.x/api/cli.html#--experimental-permission

**Permission model:**
- `--experimental-permission` enables the permission model.
- `--allow-fs-read=PATH` / `--allow-fs-write=PATH` — per-path granularity (repeated flags for multiple paths).
- `--allow-child-process` — blanket on/off, no per-executable granularity.
- `--allow-worker` — blanket on/off for worker threads.
- **No `--allow-net` equivalent.** Network access cannot be restricted through the permission model. This is a critical gap — the model can freely make HTTP requests, open sockets, etc.
- No `--deny-*` counterpart.

**Structured errors:**
- Permission denials throw errors with `code: 'ERR_ACCESS_DENIED'` and a `permission` field (e.g., `permission: 'ChildProcess'`, `permission: 'FileSystemRead'`).
- The resource path is included in the error message.

**Embeddability from Go:**
- Widely available, clean subprocess model.
- `node --experimental-permission --allow-fs-read=/tmp --allow-fs-write=/tmp script.js`.
- stdout/stderr separation works.

**Language reach:**
- JavaScript. Massive ecosystem.

**Availability:**
- Nearly universal on developer machines. `node` is everywhere.
- However, `--experimental-permission` requires Node >= 20.0.0 and is still flagged as "Active development" (Stability 1.1).

**Maturity:**
- **Experimental.** Explicitly marked as such. The API surface and behavior may change.
- Missing network restriction is a fundamental gap for sandboxing LLM code.
- C++ addons can bypass the permission model entirely.

**Rating: ⭐⭐ (Not Recommended — No Network Restriction, Experimental)**

---

### 3. Python (Subprocess + Restricted Mode)

**Permission model:**
- Python has **no built-in permission/sandbox model.**
- Options require OS-level mechanisms:
  - `seccomp-bpf` via `python-seccomp` or `prctl()` — whitelist syscalls. Very low-level, no filesystem/network granularity.
  - `chroot` / `pivot_root` — filesystem isolation but requires root.
  - Linux `namespaces` via Python's `ctypes` or `os` module — complex to wire up.
  - `restrictedpython` (PyPI) — restricts Python bytecode, but only handles attribute access, not system calls.
  - `firejail`/`bwrap` wrapper — external tool, not Python-native.

**Structured errors:**
- No structured permission error from Python itself. Would need to parse OS-level signals (SIGSYS from seccomp, EACCES from filesystem, etc.).

**Embeddability from Go:**
- `python3 script.py` — trivial. But sandboxing requires wrapping with bwrap/firejail/etc.
- stdout/stderr works, but the permission error framing is lost.

**Language reach:**
- Python — very useful for LLMs. Many code-generation tasks benefit from Python.
- But the permission model is either nonexistent or requires significant OS-level plumbing.

**Availability:**
- Python3 ubiquitous. But the sandboxing tools (seccomp, bwrap) may not be.

**Maturity:**
- Python itself: mature. The sandbox story: DIY, fragile, and hard to get right.

**Rating: ⭐ (Not Recommended — No Native Permission Model)**

---

### 4. gVisor / runsc (Container-Level Sandboxing)

**Source:** https://gvisor.dev/docs/architecture_guide/security

**Permission model:**
- Full user-space kernel implementation. Intercepts all syscalls from the sandboxed process.
- Two-layer sandbox: Sentry (user-space kernel) + host Linux seccomp/pivot_root/namespaces.
- Filesystem: controlled via OCI spec mounts. Per-directory granularity via bind mounts.
- Network: controlled via network namespace (loopback only) or host networking.
- Subprocess: controlled by the sandboxed kernel itself.
- **Extremely strong isolation** — requires exploiting both the Sentry and the host kernel to escape.

**Structured errors:**
- Standard POSIX exit codes and signals. No application-level permission error model.
- `runsc` returns exit codes; stderr contains kernel-level error messages.

**Embeddability from Go:**
- `runsc do` runs a command in a sandbox: `runsc do --host-network=false /bin/bash -c "echo hello"`.
- Requires root or appropriate capabilities (`CAP_SYS_ADMIN`) for the host.
- Requires installing `runsc` binary + platform dependencies (systrap or KVM).
- stdout/stderr: works, but `runsc do` wraps output with some log noise.

**Language reach:**
- **Polyglot** — any language that runs on Linux. This is a major advantage.
- Can sandbox bash, python, node, ruby, go — anything.

**Availability:**
- Requires `runsc` binary (can be downloaded as a release tarball).
- Requires Linux with systrap (kernel >= 4.14.77) or KVM.
- **Not available on macOS** (Linux-only). Significant gap for developer machines.
- Needs Docker/containerd integration for full OCI functionality.

**Maturity:**
- Production-grade at Google. Battle-tested in GKE Sandbox.
- However, `runsc do` convenience command is lighter-weight but gives read-only host FS by default.
- Building from source requires Bazel (complex build system).

**Rating: ⭐⭐⭐ (Strong Security, but Heavy and Linux-Only)**

---

### 5. Bubblewrap (bwrap) — Lightweight Linux Sandbox

**Source:** https://github.com/containers/bubblewrap

**Permission model:**
- Creates isolated mount, PID, IPC, UTS, network, cgroup, user namespaces.
- **Filesystem**: bind-mount specific dirs into an empty tmpfs root. `--ro-bind /usr /usr`, `--bind /tmp/sandbox /tmp`. Per-directory granularity by design — you construct the exact filesystem view.
- **Network**: `--unshare-net` gives an isolated network namespace with only loopback.
- **Subprocess**: controlled via seccomp-bpf (`--seccomp FD`) or by simply not mounting the necessary binaries.
- No application-level "permission" concept — the security model is the namespace/seccomp configuration itself.
- Requires `unprivileged_user_namespaces` enabled (most modern distros: Ubuntu, Fedora, Arch) or setuid.

**Structured errors:**
- Standard POSIX exit codes. Permission denials manifest as EACCES, EPERM, etc.
- No structured JSON error model — errors are OS-level errno values.

**Embeddability from Go:**
- Single binary, very fast startup.
- `bwrap --ro-bind / / --dev /dev --proc /proc --unshare-net -- unshare --pid -- python3 script.py`
- stdout/stderr: clean. bwrap itself doesn't inject output.
- The command-line construction is complex but deterministic (genie could build it programmatically).

**Language reach:**
- **Polyglot** — runs any Linux binary inside the sandbox.

**Availability:**
- `apt install bubblewrap` on Debian/Ubuntu.
- `dnf install bubblewrap` on Fedora.
- On macOS: **not available** (Linux-only).
- Some enterprise environments may restrict user namespaces.

**Maturity:**
- Mature, used by Flatpak for application sandboxing.
- Stable, well-maintained by the containers/ org.
- seccomp integration requires compiling eBPF programs (Kafel or libseccomp).

**Rating: ⭐⭐⭐ (Good for Polyglot, but Complex CLI and Linux-Only)**

---

### 6. nsjail — Google's Lightweight Process Isolation

**Source:** https://github.com/google/nsjail

**Permission model:**
- Uses Linux namespaces (UTS, MOUNT, PID, IPC, NET, USER, CGROUPS, TIME).
- **Filesystem**: chroot/pivot_root with per-directory bind mounts (read-only or read-write).
- **Network**: `--disable_clone_newnet` to share host network, or isolate with cloned interface.
- **Subprocess**: controlled via seccomp-bpf with Kafel policy language.
- **Resource limits**: CPU time, memory, file descriptors, process count via rlimits and cgroups.
- Configuration via protobuf config files or command-line flags.
- Example Kafel policy: `ALLOW { read, write, exit, exit_group } DEFAULT KILL` — whitelist specific syscalls.

**Structured errors:**
- Standard POSIX exit codes. SIGSYS for seccomp violations (detectable by parent).
- No JSON-structured permission errors.

**Embeddability from Go:**
- `nsjail -Mo --chroot / --user 99999 -- /bin/bash -c "echo hello"`
- Clean stdout/stderr separation.
- Requires building from source (C++, protobuf, libnl). Not a single binary download.

**Language reach:**
- **Polyglot** — any Linux binary.

**Availability:**
- **Requires building from source.** No pre-built packages in most distros.
- Dependencies: protobuf, libnl, glibc. Heavy build process.
- Not on macOS.

**Maturity:**
- Used in production at Google for CTF challenge hosting and fuzzing.
- Actively maintained. Well-documented.
- Kafel policy language is powerful but requires expertise.

**Rating: ⭐⭐⭐ (Strong, but Hard to Build and Not Widely Distributed)**

---

### 7. Firejail — Linux SUID Sandbox

**Source:** https://github.com/netblue30/firejail

**Permission model:**
- Profile-based sandboxing. Pre-built profiles for hundreds of applications.
- **Filesystem**: whitelisting approach — only specific directories visible. `--whitelist=~/work`.
- **Network**: `--net=none` disables networking. `--net=eth0` creates isolated network namespace.
- **Subprocess**: `--nodvd`, `--nosound`, `--noudev`, etc. Less granular than bwrap.
- SUID binary approach (less ideal for untrusted code execution).
- seccomp-bpf integration for syscall filtering.

**Structured errors:**
- Standard POSIX exit codes. `--deterministic-exit-code` ensures first child's exit status.
- No structured permission error model.

**Availability:**
- `apt install firejail` on Debian/Ubuntu.
- **Linux-only.**
- SUID binary: security concern in itself for some environments.

**Maturity:**
- Very mature (since 2015). Large user base.
- Profile system is powerful but was designed for desktop application sandboxing, not LLM code execution.

**Rating: ⭐⭐ (Good for Desktop Apps, Wrong Shape for Agent Code Execution)**

---

## Comparison Table

| Criterion | Deno | Node.js `--permission` | Python | gVisor/runsc | bwrap | nsjail | Firejail |
|---|---|---|---|---|---|---|---|
| **FS per-directory** | ✅ `--allow-read=/path` | ✅ `--allow-fs-read=/path` | ❌ DIY | ✅ OCI mounts | ✅ bind mounts | ✅ bind mounts | ✅ whitelists |
| **Network restriction** | ✅ `--allow-net=host:port` | ❌ **None** | ❌ DIY | ✅ net namespace | ✅ `--unshare-net` | ✅ net namespace | ✅ `--net=none` |
| **Subprocess restriction** | ✅ `--allow-run=prog` | ⚠️ On/off only | ❌ DIY | ✅ sandbox kernel | ⚠️ via seccomp | ⚠️ via seccomp | ⚠️ limited |
| **Env var restriction** | ✅ `--allow-env=VAR` | ❌ No | ❌ DIY | ✅ env isolation | ✅ mount namespace | ✅ mount namespace | ✅ `--env` |
| **Structured errors** | ✅ `NotCapable` + JSON | ⚠️ `ERR_ACCESS_DENIED` | ❌ | ❌ | ❌ | ❌ | ❌ |
| **Headless/agent mode** | ✅ `--no-prompt` + broker | ✅ no prompts | N/A | ✅ non-interactive | ✅ non-interactive | ✅ non-interactive | ✅ non-interactive |
| **Go embeddability** | ✅ single binary | ✅ single binary | ✅ single binary | ⚠️ needs root/Docker | ✅ single binary | ⚠️ complex build | ✅ single binary |
| **stdout/stderr IPC** | ✅ clean | ✅ clean | ✅ clean | ⚠️ some log noise | ✅ clean | ✅ clean | ✅ clean |
| **Language reach** | JS/TS only | JS only | Python only | **Polyglot** | **Polyglot** | **Polyglot** | **Polyglot** |
| **macOS support** | ✅ | ✅ | ✅ | ❌ Linux only | ❌ Linux only | ❌ Linux only | ❌ Linux only |
| **Ease of install** | ✅ curl + unzip | ✅ usually pre-installed | ✅ usually pre-installed | ⚠️ release tarball | ✅ apt install | ❌ build from source | ✅ apt install |
| **Maturity** | ✅ GA (Deno 2.x) | ⚠️ Experimental | N/A | ✅ Production | ✅ Stable | ✅ Production | ✅ Stable |
| **Sandbox by default** | ✅ Yes | ✅ Yes | N/A | ✅ Yes | ✅ Yes | ✅ Yes | ✅ Yes |
| **Requires root** | ❌ No | ❌ No | N/A | ⚠️ Yes (or Docker) | ❌ No* | ❌ No* | ❌ (SUID) |

*bwrap/nsjail need unprivileged user namespaces enabled (kernel config `kernel.unprivileged_userns_clone=1`).

---

## Recommendation

### Primary: Deno

Deno is the clear winner for the genie `code` tool. Reasons:

1. **Best permission model in class.** Per-directory filesystem, per-host network, per-executable subprocess, per-variable env — all independently controllable with `--allow-*` / `--deny-*` flags. No other runtime comes close to this granularity without OS-level plumbing.

2. **Structured permission errors.** `NotCapable` errors include `{ code, permission, resource }` — the harness can distinguish "permission denied" from "script error" from "timeout" with zero parsing. This is critical for the `code` tool to give the LLM a useful error message ("you tried to access /etc but that directory is not permitted" vs "your script crashed").

3. **Permission Broker for policy-driven decisions.** The harness can run a broker process over a Unix socket that makes allow/deny decisions based on per-agent or per-session policy. This enables dynamic permissions without restarting the runtime.

4. **Zero-config sandboxing.** No root, no Docker, no kernel config. Single binary download that works on Linux and macOS. The developer experience is: `curl -fsSL https://deno.land/install.sh | sh` and it works.

5. **Clean IPC.** stdout for script results, stderr for errors. `--no-prompt` suppresses all interactive prompts. The existing bashtool pattern (merged stdout+stderr with timeout) translates directly.

6. **LLM writes TypeScript/JS well.** GPT-4, Claude, Gemini all write clean TypeScript/JS. For a short-script agent harness, this is the sweet spot.

7. **Production maturity.** Deno 2.x is GA. Permission model has been stable since 2020. Permission Broker is versioned and schema-validated.

### Fallback Chain

| Priority | Runtime | When to Use |
|---|---|---|
| **P0 — Primary** | Deno | Default for all code execution. TypeScript/JavaScript scripts. |
| **P1 — Polyglot fallback** | bwrap | When the LLM needs to emit Python, Ruby, or other language scripts. Wraps any interpreter in a namespace-sandboxed environment. |
| **P2 — Maximum isolation** | gVisor/runsc | When running truly untrusted code in a production deployment. Requires root/Docker but provides kernel-level isolation. |
| **P3 — Unavailable fallback** | `sh` (current bash tool) | When neither Deno nor bwrap is installed. Identical to current behavior. |

### Implementation Sketch

The `code` tool would:

1. **Detect available runtimes** at startup: check for `deno`, then `bwrap`, then `sh`.
2. **Build permission flags** from the harness's policy configuration:
   ```
   deno run --no-prompt \
     --allow-read={cwd} \
     --allow-read={cwd}/.genie-tmp \
     --allow-write={cwd}/.genie-tmp \
     --allow-net=api.openai.com:443,api.anthropic.com:443 \
     --deny-run \
     --deny-ffi \
     -- deny-env \
     script.ts
   ```
3. **Parse structured errors** from stderr to classify failures:
   - `NotCapable` → permission denial (tell the LLM what it can't do)
   - Non-zero exit + no `NotCapable` → script error (show stderr)
   - Timeout → kill process group (same pattern as current bashtool)
4. **Write script to temp file**, pass path as argument, clean up after execution.

### Risks and Mitigations

| Risk | Mitigation |
|---|---|
| Deno not installed | Auto-install script or fall back to bwrap/sh |
| Deno's static module graph bypass | The initial static import graph loads without permission checks. Mitigate by using `--frozen` lockfile + `--cached-only` to prevent network fetches during load. |
| `--allow-run` escapes sandbox | Never grant `--allow-run` to `deno` itself. Only allow specific executables if needed. |
| Polyglot need (Python, etc.) | bwrap fallback provides namespace isolation for any interpreter |
| macOS bwrap unavailable | bwrap fallback is Linux-only; on macOS, polyglot path would need a different wrapper (or just use Deno for JS/TS) |

---

## Sources

- Deno Security Model: https://docs.deno.com/runtime/fundamentals/security/ (2026-06-17)
- Deno Permissions Reference: https://docs.deno.com/runtime/reference/permissions/ (2026-07-21)
- Node.js --experimental-permission: https://nodejs.org/docs/latest-v20.x/api/cli.html#--experimental-permission
- gVisor Security Model: https://gvisor.dev/docs/architecture_guide/security
- gVisor Introduction: https://gvisor.dev/docs/architecture_guide/intro
- Bubblewrap README: https://github.com/containers/bubblewrap/blob/main/README.md
- bwrap man page: https://manpages.debian.org/testing/bubblewrap/bwrap.1.en.html
- nsjail README: https://github.com/google/nsjail/blob/master/README.md
- Firejail man page: https://manpages.debian.org/bookworm/firejail/firejail.1.en.html
