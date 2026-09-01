# tiktoken-go Evaluation for Genie

**Date:** 2026-08-26
**Purpose:** Evaluate token counting libraries for Genie's context management (compaction, budgeting)

---

## 1. Does tiktoken-go Exist and Is It Maintained?

Yes. The canonical repo is [pkoukk/tiktoken-go](https://github.com/pkoukk/tiktoken-go) — 951 stars, 106 forks, MIT license. OpenAI lists it as the official Go tokenizer in their [cookbook](https://cookbook.openai.com/examples/how_to_count_tokens_with_tiktoken).

**Maintenance status: Effectively dormant.** The original author has not committed since May 2024. Issue [#60](https://github.com/pkoukk/tiktoken-go/issues/60) ("Is this project still active?") has been open since Jul 2025 with no maintainer response. 12 open issues, 4 open PRs, all stale.

**Forks attempting to maintain it:**
- `localit-io/tiktoken-go` — 7 stars, 74 commits, last commit Jul 2025. Adds gpt-4.1/4.5 support, removes deprecated methods. Uses `go 1.23.7`.
- `weaviate/tiktoken-go` — Weaviate's fork for their vector DB, last updated Aug 2025.

**Go version:** pkoukk requires `go 1.19`. localit-io requires `go 1.23.7`.

---

## 2. API Surface

The API is small and clean:

```go
// Get by encoding name
tke, err := tiktoken.GetEncoding("cl100k_base")

// Get by model name (model-aware)
tkm, err := tiktoken.EncodingForModel("gpt-4o")

// Encode (with special token handling)
tokens := tkm.Encode(text, nil, nil)

// Encode ordinary (no special token checking, faster)
tokens := tkm.EncodeOrdinary(text)

// Decode
text := tkm.Decode(tokens)

// Count tokens
count := len(tkm.Encode(text, nil, nil))
```

**Model-awareness:** Yes — `MODEL_TO_ENCODING` and `MODEL_PREFIX_TO_ENCODING` maps resolve model IDs to encodings. Supports gpt-4o→o200k_base, gpt-4→cl100k_base, gpt-3.5-turbo→cl100k_base, plus embeddings. Does **not** support Claude, Gemini, or any non-OpenAI models.

**Encodings available:** o200k_base, cl100k_base, p50k_base, p50k_edit, r50k_base, gpt2.

**Important limitation:** `Encode()` panics on disallowed special tokens — no graceful error return. `EncodeOrdinary()` avoids this but skips special token handling entirely.

---

## 3. Dependencies

### pkoukk/tiktoken-go
```
go 1.19
github.com/dlclark/regexp2 v1.10.0  (pure Go regex engine — Unicode-aware)
github.com/google/uuid v1.3.0        (only used in tests)
github.com/stretchr/testify v1.8.2   (test only)
```

- **Pure Go** — no CGo, no C bindings, no Rust FFI
- **Runtime deps:** Just `dlclark/regexp2` (pure Go, ~200KB)
- **Binary size impact:** ~2-4 MB for the vocab dictionaries if embedded, or ~0 if downloaded at runtime
- **Network dependency at init:** Downloads BPE vocab files from `openaipublic.blob.core.windows.net` on first use unless:
  - `TIKTOKEN_CACHE_DIR` is set (caches to disk), or
  - An offline loader is used (separate module `tiktoken-go-loader`)

### omnitoken (ron2111/omnitoken)
```
go 1.23
// Zero root dependencies — no transitive deps
```

- **Pure Go**, zero root dependencies
- Vocab files are **embedded** in the binary (no network fetch at runtime)
- Go 1.23+ required

---

## 4. Performance

### pkoukk/tiktoken-go benchmarks (from their README)

| Encoding | tiktoken-go | Python tiktoken | Ratio |
|----------|-------------|-----------------|-------|
| cl100k_base (UDHR text) | 94,502 ns | 54,642 ns | 1.7x slower |
| o200k_base (UDHR text) | 108,522 ns | 70,198 ns | 1.5x slower |

For a typical agent turn (~2K tokens of messages), expect ~2-3ms per encode. Fast enough for real-time use on every turn.

### omnitoken benchmarks

| Operation | Encoding | ns/op | Allocations |
|-----------|----------|-------|-------------|
| CountTokens | cl100k_base | 1,517 | 0 |
| EncodeOrdinary | cl100k_base | 1,661 | 1 |
| CountTokens | o200k_base | 2,152 | 0 |

**omnitoken claims 15.84x faster than tiktoken-go** on CountTokens and near-parity with Rust tiktoken. The zero-allocation hot path is significant for an agent harness doing this every turn.

---

## 5. Accuracy

### OpenAI models
tiktoken-go is a faithful Go port of OpenAI's tiktoken. Byte-identical output for the same vocabulary. The localit-io fork has added gpt-4.1 and gpt-4.5 model mappings. The library tracks OpenAI's model→encoding mapping, so new models need manual updates.

### Non-OpenAI models
**tiktoken-go has zero support for Claude, Gemini, Llama, Mistral, or any non-OpenAI tokenizer.** This is a critical limitation for Genie, which already has `internal/llm/anthropic/` and `internal/llm/google/` clients.

Key open issues on pkoukk/tiktoken-go asking for:
- Claude support ([#50](https://github.com/pkoukk/tiktoken-go/issues/50))
- Gemini support ([#49](https://github.com/pkoukk/tiktoken-go/issues/49))
- DeepSeek support ([#56](https://github.com/pkoukk/tiktoken-go/issues/56))
- Llama support ([#26](https://github.com/pkoukk/tiktoken-go/issues/26))

None addressed.

**Important nuance:** Anthropic's Claude models actually use a BPE tokenizer very close to tiktoken's `cl100k_base` (it's based on the same SentencePiece BPE). For rough token counting purposes, `cl100k_base` gives Claude estimates within ~5%. But for exact billing-parity counts, you'd need Anthropic's actual tokenizer.

---

## 6. Alternatives

### 6a. `omnitoken` (ron2111/omnitoken) — **strongest contender**

| Pros | Cons |
|------|------|
| Pure Go, zero deps | Very new (6 stars, 45 commits) |
| Embedded vocab (no network) | Small community, unproven at scale |
| Multi-provider: OpenAI, Gemini, Llama, Mistral, HuggingFace, Anthropic adapters | Some adapters are optional modules |
| 15x faster than tiktoken-go | |
| Zero-allocation CountTokens | |
| Cacheflow analysis built-in | |
| Go 1.23+ | |

**The omnitoken adapter list is exactly what Genie needs:**
- OpenAI BPE (cl100k_base, o200k_base, o200k_harmony)
- Anthropic message counter (optional module)
- Gemini local text adapter (optional module)
- Llama 3 tiktoken-BPE adapter (optional module)
- Mistral Tekken adapter (optional module)

### 6b. `j178/tiktoken-go` — Pure Go, embedded vocab

Embeds vocabularies at build time (~4MB binary increase). No runtime downloads. But only 1 star, last commit 2024, doesn't handle special tokens, no non-OpenAI support.

### 6c. `runtoken` (Thibaultjaigu/runtoken) — Rust, not Go

Rust implementation with Python bindings. 20-80x faster than tiktoken. Not usable from Go without CGo/Rust FFI bindings. Ruled out.

### 6d. Heuristic approximation

A simple `len(text) / 4` or `strings.Fields(text) * 1.33` estimator. Zero dependencies. ±20-30% accuracy. Could be a pragmatic fallback for budgeting when exact counts aren't critical.

---

## 7. Integration with Genie

### Current Genie architecture

From `internal/llm/llm.go`:

```go
type Model struct {
    ID string  // <-- Only field, no ContextLength yet
}

type Client interface {
    Stream(ctx context.Context, req Request) (iter.Seq[Event], error)
    ListModels(ctx context.Context) ([]Model, error)
}
```

The agent loop in `internal/agent/agent.go` builds messages, streams replies, and handles tool calls in a loop. There is **no token counting** today — the `Usage` event from the stream is only logged, not used for budgeting.

### What's needed for context management

To add compaction/budgeting, Genie needs:
1. A way to count tokens in a `[]llm.Message` slice
2. A model→tokenizer mapping (model-aware)
3. A context window budget (requires `ContextLength` on `Model` or config)
4. A compaction strategy (e.g., drop oldest messages when budget exceeded)

### Concrete integration options

#### Option A: tiktoken-go directly
```go
// Pro: simple, well-known API
// Con: OpenAI-only, network fetch at init, dormant maintenance
tke, _ := tiktoken.EncodingForModel(model)
count := len(tke.Encode(text, nil, nil))
```

#### Option B: omnitoken
```go
// Pro: multi-provider, embedded vocab, fast, zero deps
// Con: new library, small community
engine, _ := omnitoken.ForModel("gpt-4o")
count := engine.CountTokens(text)
```

#### Option C: Internal abstraction + pluggable backends
```go
// internal/tokens/tokens.go
type Counter interface {
    CountMessages([]llm.Message) (int, error)
    CountText(text string) int
}
// Implement with tiktoken-go, omnitoken, or heuristic per-model
```

This is the Genie-idiomatic approach: define the seam at the interface level, let the implementation vary. The `Counter` could be:
- tiktoken-go for OpenAI models
- A heuristic for unsupported models (Claude via cl100k_base approximation)
- omnitoken for multi-provider coverage

---

## Recommendation

### Short-term: `omnitoken`

**Why:**
- Genie is a multi-provider harness (OpenAI, Anthropic, Google, plugins). OpenAI-only token counting is insufficient.
- omnitoken's adapter list maps directly to Genie's provider list.
- Zero root dependencies fits Genie's "zero runtime deps beyond toml" philosophy.
- Embedded vocab = no network fetch = works offline, no init surprise.
- 15x faster than tiktoken-go, zero allocations on the hot path.

**Risk:** New library, small community. Mitigate by wrapping behind an interface so it can be swapped.

### Concrete next steps

1. Add `ContextLength int` to `llm.Model` struct
2. Add `internal/tokens` package with a `Counter` interface
3. Implement omnitoken-backed `Counter` with model→engine cache
4. Add a `CountMessages([]llm.Message, model string) int` helper that accounts for role overhead per the OpenAI cookbook formula
5. Wire into the agent loop for pre-flight budget checks and post-turn compaction

### If you want minimal change first

Use tiktoken-go (pkoukk or localit-io fork) for OpenAI models only, and add a `len(text)/4` heuristic fallback for non-OpenAI models. This gets you token counting with minimal code change and no new architectural surfaces. Upgrade to omnitoken later when multi-provider accuracy matters.

---

## Dependency comparison summary

| Library | Pure Go | CGo | Root deps | Vocab source | Go version | Stars | Last commit |
|---------|---------|-----|-----------|-------------|------------|-------|-------------|
| pkoukk/tiktoken-go | Yes | No | regexp2, uuid | Download | go 1.19 | 951 | May 2026 (merge) |
| localit-io/tiktoken-go | Yes | No | regexp2, uuid | Download | go 1.23.7 | 7 | Jul 2025 |
| omnitoken | Yes | No | **Zero** | **Embedded** | go 1.23 | 6 | Active |
| j178/tiktoken-go | Yes | No | None | **Embedded** | ? | 1 | 2024 |
| runtoken | N/A | Rust | N/A | Embedded | N/A | 4 | Active (Python/Rust) |
