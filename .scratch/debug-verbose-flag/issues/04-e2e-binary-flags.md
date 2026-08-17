# 04 — E2e tests (binary flags)

**What to build:** comprehensive e2e tests in `internal/e2e` that exercise the `-v`, `-d`, and `OG_DEBUG` flags through the compiled binary, reusing the existing `fake.Provider` and `TestMain` build pattern. Tests pass flag combinations and assert on stderr content and stdout emptiness. Verify the startup banner appears, verbose messages appear with `-v`, debug messages appear with `-d`, nothing appears without flags, stdout is unaffected by any flag combination, and the API key never leaks into stderr.

**Blocked by:** 01 — Slog infrastructure; 02 — Verbose logging; 03 — Debug logging.

**Status:** ready-for-agent

- [ ] E2e: `-v` produces banner and verbose messages on stderr; stdout is unaffected
- [ ] E2e: `-d` produces banner and debug messages (including verbose) on stderr; stdout is unaffected
- [ ] E2e: no flags → stderr is empty
- [ ] E2e: `OG_DEBUG=true` enables debug output equivalent to `-d`
- [ ] E2e: `-d` overrides a false `OG_DEBUG` — either active enables debug
- [ ] E2e: API key never appears in stderr with `-v`, `-d`, or `OG_DEBUG=1`
- [ ] E2e: stdout is identical regardless of `-v`, `-d`, or `OG_DEBUG` flags
