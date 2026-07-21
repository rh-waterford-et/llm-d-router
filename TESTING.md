# TESTING.md

## Test Tiers

| Tier | Functions | Files | Purpose |
|---|---|---|---|
| Unit: Framework & Plugins | 742 | 121 | Plugin contracts, scheduling logic, parsers, flow control plugins |
| Unit: Flow Control | 93 | 20 | Queue admission, eviction, fairness, ordering, registry |
| Unit: Sidecar | 41 | 17 | P/D sidecar proxy logic |
| Unit: EPP Core | 120 | 18 | Datastore, config loading, metrics, scheduling engine, request control |
| Unit: Common | 22 | 10 | Envoy chunking, path matching, shared utilities |
| Integration (hermetic) | 24 | 12 | Full EPP pipeline in-process via envtest, no cluster required |
| Integration (cluster) | 24 | 12 | Same suite against a real Kubernetes cluster (`KUBECONFIG` required) |
| Integration (prediction server) | ~30 | 1 | Latency predictor client against a live prediction server (`PREDICTION_SERVER_URL` required) |
| E2E: Router | 1 suite | 6 | Pod disruption, scale-to-zero, routing correctness on kind |
| E2E: Sidecar | 1 suite | 2 | P/D disaggregation sidecar against a live cluster |
| Benchmark: Tokenizer | 27 | 1 | Tokenizer throughput profiling |
| Benchmark: Flow Control | ~10 | 1 | Flow control subsystem throughput |

**Total test functions: ~1,110** across **239 test files**.

---

## Test Structure

```
test/
├── e2e/
│   ├── e2e_suite_test.go          — Ginkgo suite bootstrap
│   ├── e2e_test.go                — core routing scenarios
│   ├── disruption_test.go         — pod kill, EPP restart, scale-to-zero
│   ├── configs_test.go            — config smoke tests
│   ├── requests_test.go           — request shape variations
│   ├── setup_test.go              — cluster and image setup helpers
│   └── utils_test.go              — shared e2e utilities
├── integration/
│   ├── suite_test.go              — suite entry point (build tag: integration_tests)
│   ├── epp_test.go                — EPP integration entry (build tag: integration_tests)
│   └── epp/
│       ├── harness.go             — in-process EPP server harness (envtest)
│       ├── hermetic_test.go       — HTTP path: malformed JSON, streaming, buffering
│       ├── grpc_test.go           — gRPC path: protocol boundaries, error handling
│       ├── session_affinity_test.go
│       ├── datalayer_integration_test.go
│       ├── dynamic_attributes_test.go
│       ├── k8s_datasource_integration_test.go  — skipped under -short
│       ├── runtime_notification_test.go
│       ├── runtime_polling_test.go              — skipped under -short
│       ├── request_attribute_reporter_test.go
│       ├── request_attribute_reporter_streaming_test.go
│       ├── wellknown_configs_test.go
│       └── e2e_config_smoke_test.go
├── sidecar/
│   ├── e2e/                       — P/D sidecar end-to-end tests
│   ├── mock/                      — mock chat completions and generic handlers
│   └── utils/                     — shared sidecar test utilities
├── profiling/
│   └── tokenizerbench/
│       └── benchmark_test.go      — 27 tokenizer benchmark functions
├── scripts/
│   ├── e2e-common.sh
│   └── test-e2e-router.sh
└── testdata/                      — shared YAML fixtures

pkg/
├── common/                        — 22 functions, 10 files
│   └── envoy/                     — chunking (at-limit, off-by-one), path/header matching
├── epp/
│   ├── flowcontrol/               — 93 functions, 20 files
│   │   ├── integration_test.go    — capacity, zombie TTL, concurrent saturation reads
│   │   ├── benchmark/             — flow control throughput benchmarks
│   │   ├── config_test.go         — nil config handling
│   │   ├── controller/            — exactly-once finalization, concurrency shutdown race
│   │   ├── eviction/              — eviction queue PopN bounds, evictor nil safety
│   │   ├── framework/plugins/     — queue conformance, EDF/SLO deadline ordering
│   │   └── registry/              — leasing lifecycle, managed queue delta invariants
│   ├── framework/                 — 742 functions, 121 files
│   │   ├── interface/             — type system (MaxUint32 bounds, NaN, negative IDs)
│   │   └── plugins/
│   │       ├── scheduling/
│   │       │   ├── filter/        — bylabel, modality, prefixcacheaffinity, sloheadroomtier
│   │       │   ├── scorer/        — nohitlru, prefix cache, session affinity scorers
│   │       │   └── picker/        — maxscore, weightedrandom zero-weight edge case
│   │       ├── requestcontrol/
│   │       │   ├── admitter/      — probabilisticadmitter (zero/negative thresholds)
│   │       │   ├── preadmitter/   — agent identity
│   │       │   └── dataproducer/  — predictedlatency, inflightload underflow, tokenizer, burstprefix
│   │       ├── flowcontrol/       — saturation detectors, EDF/SLO ordering, fairness
│   │       └── requesthandling/parsers/
│   │                              — openai, anthropic, vllmhttp, vertexai, grpc
│   ├── datastore/                 — 20 functions, 2 files — concurrent pool ops, active-port parsing
│   ├── config/                    — 12 functions, 4 files — loader defaults, nil DataLayer injection
│   ├── metrics/                   — 24 functions, 3 files
│   ├── scheduling/                — 14 functions, 4 files — scheduler engine
│   └── requestcontrol/            — 26 functions, 4 files — request lifecycle director
└── sidecar/                       — 41 functions, 17 files
    └── ...                        — P/D proxy, sidecar integration tests
```

---

## Running Tests

### Standard targets (run inside the builder container)

```bash
make test                       # unit + e2e
make test-unit                  # unit only (epp + sidecar)
make test-coverage              # unit with coverage output
make presubmit                  # full pre-merge gate: format, lint, unit tests
```

### Integration tests

```bash
# Hermetic — in-process envtest, no cluster required
make test-integration-hermetic

# Against a real cluster (requires KUBECONFIG)
make test-integration
```

The integration suite is gated by a build tag — it does not run with a plain `go test ./...`:

```bash
# Equivalent manual invocation
go test -tags integration_tests ./test/integration/...
```

Several tests within the integration suite skip when `-short` is set:

```bash
go test -short -tags integration_tests ./test/integration/...
# skips: k8s_datasource_integration_test.go, runtime_polling_test.go (5 tests)
```

### E2E tests

```bash
make test-e2e                   # builds images then runs e2e suite
make test-e2e-run               # skips build, runs against pre-pulled images
```

### Prediction server integration (env-gated)

Tests in `pkg/epp/framework/plugins/requestcontrol/dataproducer/predictedlatency/latencypredictorclient/` skip automatically when the required env vars are absent:

```bash
PREDICTION_SERVER_URL=http://... TRAINING_SERVER_URL=http://... go test ./pkg/epp/framework/plugins/requestcontrol/dataproducer/predictedlatency/latencypredictorclient/...
```

### Targeted runs

```bash
make test-filter PATTERN=TestShardProcessor TYPE=epp
make test-filter PATTERN=TestOpenAIParser   TYPE=epp
```

### Benchmarks

```bash
# Tokenizer
cd test/profiling/tokenizerbench && go test -bench=. -benchmem

# Flow control
cd pkg/epp/flowcontrol/benchmark && go test -bench=. -benchmem
```

---

## Tier Details

### Tier 1 — Unit: Framework & Plugins
**Location:** `pkg/epp/framework/` | **Run:** `make test-unit`

| Category | What is tested |
|---|---|
| Parsers | OpenAI, Anthropic, vLLM HTTP, VertexAI gRPC — valid paths, nil/empty bodies, malformed JSON, invalid formats |
| Type system | Token ID bounds (MaxUint32 accepted, MaxUint32+1 rejected), negative values, NaN, non-integers |
| Filters | `bylabel`, `modality`, `prefixcacheaffinity`, `sessionaffinity`, `sloheadroomtier` — nil/empty endpoint slices, out-of-range config parameters |
| Scorers | `nohitlru`, `prefix`, `preciseprefixcache`, `sessionaffinity` — nil/empty slices, zero-weight exclusion |
| Admitters | `probabilisticadmitter` — zero and negative thresholds, power, and K values |
| Data producers | `inflightload` underflow guards, `predictedlatency` range validation (0.0, 1.0, negative, over-max), `tokenizer` byte-boundary straddling |
| Saturation detectors | `concurrency` and `utilization` — invalid headroom, invalid thresholds, saturation caps at 1.0, empty candidate list (fail-closed) |
| Path matching | Segment-boundary (`/`), gRPC dot-boundary (`.`), trailing slashes |

### Tier 2 — Unit: Flow Control
**Location:** `pkg/epp/flowcontrol/` | **Run:** `make test-unit`

| Category | What is tested |
|---|---|
| Boundary values | Exactly at capacity, one over, one under — for shard bytes, shard requests, band bytes, band requests |
| Capacity enforcement | Per-band and global byte limits; global rejects even when per-band has room |
| Concurrent reads | Saturation value stays in `[0.0, 1.0]` under concurrent goroutine reads; returns to exactly 0 after all ops complete |
| Exactly-once finalization | Dispatch vs. expiry race: finalization handler called exactly once regardless of race outcome |
| Concurrent shutdown | No races or panics when requests enqueue concurrently with controller shutdown |
| Nil safety | `Submit(nil)`, double-evict, evict with nil channel |
| Zombie capacity | Expired TTL items hold capacity until cleanup sweep, falsely rejecting new requests |
| Queue conformance | Invalid handle, nil handle, peek on empty, cleanup on empty, drain on empty |
| Delta invariants | Propagated length and byte-size deltas exactly match queue size changes across all operations (add, remove, cleanup, drain) |
| Ordering | EDF and SLO deadline nil-item ordering (a nil, b nil, both nil) |

### Tier 3 — Unit: Sidecar
**Location:** `pkg/sidecar/` | **Run:** `make test-unit`

41 test functions across 17 files covering the P/D disaggregation sidecar proxy, connection handling, and version negotiation.

### Tier 4 — Unit: EPP Core
**Location:** `pkg/epp/datastore/`, `pkg/epp/config/`, `pkg/epp/metrics/`, `pkg/epp/scheduling/`, `pkg/epp/requestcontrol/` | **Run:** `make test-unit`

| Category | What is tested |
|---|---|
| Datastore | Concurrent pool set with nil, empty active-ports annotation, invalid/negative port values, no-deadlock under concurrent write |
| Config loading | Nil DataLayer injects defaults, zero MaxBytes, missing fields default correctly |
| Scheduling engine | Profile selection, weighted scorer composition |
| Request control | Lifecycle sequencing (PreAdmit → DataProduce → Admit → Schedule → PreRequest → Response) |

### Tier 5 — Unit: Common
**Location:** `pkg/common/` | **Run:** `make test-unit`

| Category | What is tested |
|---|---|
| Chunking | Body at limit, one below, one above, exactly 2x limit — off-by-one coverage of `BodyByteLimit` |
| Path matching | Segment-boundary matching, gRPC dot-boundary, trailing slashes, nil headers map |

### Tier 6 — Integration: Hermetic
**Location:** `test/integration/epp/` | **Run:** `make test-integration-hermetic`

Full EPP server started in-process using `envtest` via `test/integration/epp/harness.go`. No Kubernetes cluster required. Tests send raw ext-proc requests and assert on the EPP response — no mocking of the scheduler or plugin chain.

- Malformed JSON body through the full HTTP path
- Invalid gRPC payload and unsupported gRPC method at the protocol boundary
- Streaming response buffering with invalid JSON and empty EOS chunks
- Session affinity across multiple requests
- Dynamic attribute updates mid-stream
- Data layer polling and k8s notification sources
- Well-known config variants (standalone, standard, with/without CRDs)

### Tier 7 — Integration: Cluster
**Location:** `test/integration/epp/` | **Run:** `make test-integration`

Same suite as Tier 6, run against a real Kubernetes cluster. Requires `KUBECONFIG` set and CRDs installed. Build tag: `integration_tests`.

### Tier 8 — Integration: Prediction Server
**Location:** `pkg/epp/framework/plugins/requestcontrol/dataproducer/predictedlatency/latencypredictorclient/`

Gated on env vars — all tests call `t.Skip` when `PREDICTION_SERVER_URL` is unset. Tests bulk prediction, prefix cache integration, HTTP fallback, performance benchmarking, and training data flushing against a live predictor service.

```bash
PREDICTION_SERVER_URL=http://... TRAINING_SERVER_URL=http://... go test ./pkg/.../latencypredictorclient/...
```

### Tier 9 — E2E: Router
**Location:** `test/e2e/` | **Run:** `make test-e2e`

Ginkgo suite against a kind cluster with a real EPP, Envoy, and inference simulators:

| Scenario | Assertion |
|---|---|
| Pod killed mid-request | EPP recovers and routes subsequent requests |
| Pod killed during streaming | Request does not hang |
| All pods gone | Returns 503 |
| EPP killed during inflight requests | Recovers after restart |
| Scale-to-zero and back | 503s during empty pool; traffic resumes after scale-up |
| Config variations | Well-known configs produce expected routing behavior |

### Tier 10 — E2E: Sidecar
**Location:** `test/sidecar/e2e/` | **Run:** `make test-e2e` (sidecar target)

End-to-end tests for the P/D disaggregation sidecar. Requires a live cluster with prefill and decode pods. Validates KV-cache transfer and decode pod coordination.

### Tier 11 — Benchmarks
**Locations:**
- `test/profiling/tokenizerbench/` — 27 functions, tokenizer throughput across request sizes
- `pkg/epp/flowcontrol/benchmark/` — flow control subsystem throughput

Not gated in CI. Run manually to profile EPP CPU budget before and after changes to the scheduler hot path.

---

## Key Patterns

### Boundary value pattern (flow control)

Tests at admission boundaries use an explicit comment convention:

```go
// --- Boundary value tests ---
{
    name: "should allow when shard bytes exactly at capacity after add",
    // bytes == capacity
},
{
    name: "should deny when shard bytes one over capacity after add",
    // bytes == capacity + 1
},
```

Covers shard bytes, shard requests, band bytes, and band requests — eight cases in total.

### Nil/empty slice safety (scheduling plugins)

All filters and scorers open with nil and empty endpoint slice cases before any functional cases:

```go
{"empty endpoints slice", []Endpoint{}, ...},
{"nil endpoints slice", nil, ...},
```

### Concurrency guard pattern (flow control)

Race conditions are tested with explicit goroutine barriers and invariant assertions:

```go
// TestConcurrentSaturationReads
// Use a start barrier to ensure both goroutines begin concurrently.
assert.InDelta(t, 0.0, saturation, 0.001,
    "saturation must return to exactly 0 after all concurrent operations complete")
```

Exactly-once finalization is tested by racing dispatch and expiry simultaneously, then asserting the finalization handler fires exactly once.

### Delta invariant pattern (managed queue)

Queue mutation operations assert that propagated size deltas exactly match the actual size change — catches off-by-one errors in accounting:

```go
assert.Equal(t, expectedDelta, propagatedLengthDelta,
    "The propagated length delta must exactly match the change in queue size")
```

### Underflow guard pattern (in-flight load)

The in-flight request counter is stress-tested under concurrent add/remove and simulated crash scenarios to verify it never goes negative:

```go
// TestInFlightLoadProducer_FlapDoesNotUnderflow
// TestInFlightLoadProducer_CrashWithHighLoadDoesNotUnderflow
```

### Hermetic integration harness

`test/integration/epp/harness.go` starts a real EPP gRPC server in-process with configurable mode (standalone or standard) and optional tracing. Tests send raw ext-proc request streams and assert on the EPP response — no mocking of the scheduler or plugin chain:

```go
h := NewTestHarness(t, WithStandaloneMode(...), WithConfigText(yaml))
resp := h.SendRequest(req)
```

---

## Gaps & Known Limitations

| Gap | Severity | Notes |
|---|---|---|
| Prediction server tier not in CI | High | `PREDICTION_SERVER_URL`-gated tests (~30 functions) never run in standard PR gate; latency predictor regressions ship silently |
| Sidecar e2e requires live GPU cluster | High | Kind-based sidecar tests exist but full P/D KV-cache transfer requires real hardware |
| Active-Active HA + prefix routing | Medium | No automated test for cache-hit degradation under multi-replica EPP; covered only in docs |
| Multimodal routing (TTS/STT) | Medium | Modality filter unit tests exist; no e2e against a real TTS/STT model endpoint |
| E2E suite is a single Ginkgo `It` | Low | `test/e2e/e2e_test.go` — all scenarios in one block; individual scenario failures cannot be isolated or re-run independently |
| Flow control benchmarks not in CI | Low | `pkg/epp/flowcontrol/benchmark/` runs manually only; no throughput regression gate |
| `testing.Short()` skips undocumented | Low | 5+ integration tests skip silently under `-short`; no CI job documents which tier this corresponds to |

---

## CI Schedule

| Job | Trigger | Make target |
|---|---|---|
| Presubmit | Every PR | `make presubmit` |
| Unit tests | Every PR | `make test-unit` |
| Hermetic integration | Every PR | `make test-integration-hermetic` |
| E2E | Every PR (images required) | `make test-e2e` |
| Cluster integration | On-demand / nightly | `make test-integration` |
| Sidecar e2e | On-demand | `make test-e2e` (sidecar) |
| Prediction server integration | Manual only | `PREDICTION_SERVER_URL=... go test ./pkg/.../latencypredictorclient/...` |
