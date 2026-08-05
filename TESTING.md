# Testing Guide

Single reference for the llm-d-router test suite. Covers every tier, how to run it, what it tests, and how to add new tests.

## Overview

The project uses a multi-tier testing strategy spanning unit tests, BDD behavioral specs, hermetic and cluster integration tests, end-to-end suites, acceptance tests, and benchmarks. Tests run inside a builder container; host Go is not required.

| Tier | Description | Files | Runner |
|------|-------------|-------|--------|
| Unit: Framework & Plugins | Plugin contracts, scheduling logic, parsers, flow control | ~121 | `make test-unit-epp` |
| Unit: Flow Control | Queue admission, eviction, fairness, ordering | ~20 | `make test-unit-epp` |
| Unit: Sidecar | P/D sidecar proxy, connection handling | ~17 | `make test-unit-sidecar` |
| Unit: EPP Core | Datastore, config, metrics, scheduling engine | ~18 | `make test-unit-epp` |
| Unit: Common | Envoy chunking, path matching, utilities | ~10 | `make test-unit-epp` |
| BDD: Scheduling | Ginkgo behavioral specs for the scheduler | new | `make test-bdd` |
| BDD: Parsers | Ginkgo cross-parser behavioral specs | new | `make test-bdd` |
| BDD: Flow Control | Ginkgo behavioral specs for admission/saturation | new | `make test-bdd` |
| Integration: Hermetic | Full EPP pipeline via envtest, no cluster | ~12 | `make test-integration-hermetic` |
| Integration: Cluster | Same suite against a real cluster | ~12 | `make test-integration` |
| E2E: Router | Ginkgo against kind cluster with Envoy + simulators | ~6 | `make test-e2e` |
| E2E: Coordinator | Ginkgo coordinator pipeline tests | ~6 | `make test-e2e` |
| E2E: Sidecar | P/D disaggregation against live cluster | ~2 | `make test-e2e` |
| Acceptance: Gherkin | Godog feature files with step definitions | new | `make test-acceptance` |
| Benchmark: Tokenizer | Tokenizer throughput profiling | 1 | `make bench-tokenizer` |
| Benchmark: Flow Control | Flow control subsystem throughput | 1 | manual |

## Test Structure

```
pkg/                                 # Unit tests co-located with production code
  common/                            # Envoy, certs, logging, routing, request
  epp/
    config/                          # Config loading
    controller/                      # K8s reconciler
    datalayer/                       # Data graph, factory, manager
    datastore/                       # Datastore, model rewrite
    flowcontrol/                     # Queue, eviction, fairness, ordering
      benchmark/                     # Flow control benchmarks
    framework/
      interface/                     # Interface tests
      plugins/
        datalayer/                   # Attribute, discovery, extractor, source
        flowcontrol/                 # Eviction, fairness, ordering, saturation, limits
        requestcontrol/              # Admitters, data producers, request headers
        requesthandling/parsers/     # OpenAI, Anthropic, VertexAI, passthrough, vLLM
        scheduling/                  # Filters, pickers, scorers, profile handlers
    metrics/                         # Prometheus metric tests + testdata fixtures
    scheduling/                      # Scheduler engine + BDD specs
    server/                          # Controller config, gRPC, metrics, options
    util/                            # Pod, env, request header utils
  coordinator/                       # Coordinator pipeline, gateway, server
  sidecar/proxy/                     # Sidecar unit tests (Ginkgo BDD)

test/
  acceptance/                        # Godog Gherkin acceptance tests
    features/
      scheduling/                    # Routing, filter chain features
      parsing/                       # OpenAI, Anthropic features
      flowcontrol/                   # Admission features
  e2e/                               # Router E2E (Ginkgo + kind)
  coordinator/e2e/                   # Coordinator E2E
  sidecar/e2e/                       # Sidecar E2E
  integration/                       # Hermetic + cluster integration
    epp/                             # Hermetic integration test files
  ears/                              # EARS requirement package
  profiling/tokenizerbench/          # Tokenizer benchmarks
  utils/                             # Shared test utilities
  scripts/                           # E2E runner scripts
  testdata/                          # Shared YAML fixtures
```

## Running Tests

### Primary Targets

| Target | What it runs |
|--------|-------------|
| `make test` | Unit + E2E |
| `make test-unit` | All unit tests (EPP + sidecar) |
| `make test-unit-epp` | EPP unit tests with coverage |
| `make test-unit-sidecar` | Sidecar unit tests with coverage |
| `make test-bdd` | BDD behavioral specs (Ginkgo) |
| `make test-acceptance` | Gherkin acceptance tests (godog) |
| `make test-all` | Unit + BDD + acceptance |
| `make test-integration-hermetic` | Hermetic integration (envtest) |
| `make test-integration` | Cluster integration (requires KUBECONFIG) |
| `make test-e2e` | Build images + run E2E |
| `make test-e2e-run` | Run E2E (images already available) |
| `make test-coverage` | Unit tests with coverage output |
| `make presubmit` | Format + lint + vulncheck + latest-tags |

### Targeted Runs

Run a specific test by name pattern and component:

```bash
make test-filter PATTERN=TestSchedule TYPE=epp
make test-filter PATTERN=TestProxy TYPE=sidecar
```

Run hermetic integration with a pattern filter:

```bash
make test-integration-hermetic PATTERN=TestHermetic
```

### Integration Tests

Hermetic integration tests use envtest (no cluster required):

```bash
make test-integration-hermetic
```

Cluster integration tests require `KUBECONFIG` and installed CRDs, gated by the `integration_tests` build tag:

```bash
make test-integration
```

### E2E Tests

E2E tests create a kind cluster and deploy real components. Environment variables:

| Variable | Default | Purpose |
|----------|---------|---------|
| `E2E_NUM_PROCS` | 5 | Ginkgo parallel processes |
| `E2E_KEEP_CLUSTER_ON_FAILURE` | false | Keep kind cluster on test failure |
| `NAMESPACE` | (generated) | Kubernetes namespace |
| `HF_TOKEN` | (none) | HuggingFace token for model pulls |

### Benchmarks

```bash
make bench-tokenizer                    # Tokenizer throughput (requires kind + EPP)
go test -bench=. -benchmem ./pkg/epp/flowcontrol/benchmark/  # Flow control (in builder)
go test -bench=. -benchmem ./pkg/epp/scheduling/             # Scheduler (in builder)
```

### Acceptance Tests

Gherkin acceptance tests use godog with `.feature` files:

```bash
make test-acceptance
```

Run a specific scenario:

```bash
go test -v -run "TestAcceptance/Route_to_the_least-loaded_endpoint" ./test/acceptance/...
```

## Tier Details

### Unit: Framework & Plugins

Tests in `pkg/epp/framework/plugins/` cover the plugin model. Each plugin type (filter, scorer, picker, admitter, data producer, saturation detector) has tests verifying its contract.

Parsers in `pkg/epp/framework/plugins/requesthandling/parsers/` test request parsing for OpenAI, Anthropic, VertexAI, passthrough, and vLLM formats. Each parser test verifies model extraction, prompt/message parsing, error handling for malformed input, and response format consistency.

Scheduling plugin tests in `pkg/epp/framework/plugins/scheduling/` cover filters (by-label, modality, prefix cache affinity, session affinity, SLO headroom), scorers (KV cache, queue depth, prefix, LoRA affinity, session affinity, active requests, load-aware), and pickers (max score, random, weighted random).

### Unit: Flow Control

Tests in `pkg/epp/flowcontrol/` verify queue admission, eviction, fairness, and ordering. Boundary value testing uses an explicit convention with cases covering exactly-at-capacity, one-over, and one-under for shard bytes, shard requests, band bytes, and band requests.

Key patterns:
- Concurrent saturation reads verify the value stays in [0.0, 1.0]
- Exactly-once finalization races dispatch against expiry
- Delta invariants ensure propagated deltas exactly match queue size changes
- Zombie capacity tests verify expired TTL items hold capacity until cleanup

### Unit: Sidecar

Tests in `pkg/sidecar/proxy/` use Ginkgo BDD (the canonical BDD pattern in this repo). They cover the P/D disaggregation sidecar proxy including connection handling, version negotiation, allowlist validation, and connector variants (NIXL, shared storage, Mooncake, P2P).

### Unit: EPP Core

Tests in `pkg/epp/datastore/`, `pkg/epp/config/`, `pkg/epp/metrics/`, `pkg/epp/scheduling/` cover the core EPP infrastructure. Datastore tests cover concurrent pool set with nil, empty active-ports annotation, and invalid port values. Scheduling engine tests cover profile selection and weighted scorer composition.

### Unit: Common

Tests in `pkg/common/` cover Envoy chunking (body at limit, one below, one above, exactly 2x), path matching (segment boundaries, gRPC dots, trailing slashes), TLS certificate handling, logging configuration, and request token utilities.

### BDD: Behavioral Specs

Ginkgo BDD specs complement existing unit tests with structured behavioral specifications. Located in the same packages as the unit tests, named `*_bdd_test.go`.

Scheduling BDD specs (`pkg/epp/scheduling/scheduling_bdd_test.go`) cover:
- No candidate endpoints returns an error
- Single endpoint selection
- Multi-endpoint scoring and selection
- Filter elimination behavior

Parser BDD specs (`pkg/epp/framework/plugins/requesthandling/parsers/parsers_bdd_test.go`) cover:
- Cross-parser model extraction
- Chat completions message parsing
- Malformed JSON error handling
- Empty body handling

Flow control BDD specs (`pkg/epp/flowcontrol/flowcontrol_bdd_test.go`) cover:
- Saturation detection contracts
- Boundary value admission

### Integration: Hermetic

Tests in `test/integration/epp/` start a real EPP gRPC server in-process via envtest. The `TestHarness` builder pattern configures the server with options like `WithStandardMode()`, `WithStandaloneMode()`, `WithConfigText()`, `WithTracing()`. Tests send raw ext-proc requests and assert on EPP responses without mocking the scheduler or plugin chain.

Scenarios include malformed JSON, invalid gRPC payloads, streaming response buffering, session affinity, dynamic attribute updates, data layer polling, Kubernetes notification sources, request attribute reporting, and well-known config variants.

### Integration: Cluster

The same suite as hermetic integration but against a real Kubernetes cluster. Requires `KUBECONFIG` and installed CRDs. Gated by the `integration_tests` build tag.

### E2E: Router

A Ginkgo suite in `test/e2e/` against a kind cluster with real EPP, Envoy, and inference simulators. Scenarios:
- EPP recovery after a pod is killed mid-request
- Streaming requests don't hang after pod death
- 503 responses when all pods are gone
- EPP recovery after being killed during inflight requests
- Scale-to-zero produces 503s with traffic resuming after scale-up
- Well-known configs produce expected routing behavior

Uses `testWrapper()` for lifecycle management (namespace creation, Envoy deployment, RBAC setup, cleanup).

### E2E: Coordinator

A Ginkgo suite in `test/coordinator/e2e/coordinator/` testing the coordinator pipeline. Flat list of `It` blocks under a `Describe`, each calling `runCoordinatorPipeline()` for setup/teardown.

### E2E: Sidecar

A Ginkgo suite in `test/sidecar/e2e/` testing P/D disaggregation against a live cluster with prefill and decode pods.

### Acceptance: Gherkin

Godog acceptance tests in `test/acceptance/` use Gherkin `.feature` files with Go step definitions. Feature files describe user-facing behaviors in Given/When/Then format.

Feature areas:
- `features/scheduling/` - Basic routing, filter chain
- `features/parsing/` - OpenAI requests, Anthropic requests
- `features/flowcontrol/` - Admission and saturation

### Benchmarks

Tokenizer benchmarks in `test/profiling/tokenizerbench/` profile throughput across request sizes. Flow control benchmarks in `pkg/epp/flowcontrol/benchmark/` profile subsystem throughput. Scheduler benchmarks in `pkg/epp/scheduling/scheduler_bench_test.go` profile the scheduler hot path.

## Key Patterns

### Table-Driven Tests

The dominant unit test pattern. A slice of test structs with `name`, inputs, and expected outputs, iterated with `t.Run()`:

```go
tests := []struct {
    name    string
    input   []fwksched.Endpoint
    wantErr bool
}{
    {name: "no endpoints", input: nil, wantErr: true},
    {name: "single endpoint", input: endpoints, wantErr: false},
}
for _, tc := range tests {
    t.Run(tc.name, func(t *testing.T) { ... })
}
```

### Boundary Value Pattern

Flow control admission tests explicitly cover exactly-at-capacity, one-over, and one-under for each resource dimension.

### Nil/Empty Slice Safety

All scheduling filters and scorers begin with nil and empty endpoint slice test cases before functional tests.

### Concurrency Guard

Race conditions are tested with goroutine barriers and invariant assertions. The concurrency saturation detector verifies the saturation value stays in [0.0, 1.0] under concurrent access.

### Delta Invariant

Queue mutations assert that propagated length deltas exactly match queue size changes, catching off-by-one accounting errors.

### Underflow Guard

The in-flight request counter undergoes stress testing under concurrent add/remove to ensure it never goes negative.

### Hermetic Integration Harness

The `TestHarness` in `test/integration/epp/harness.go` starts a real EPP gRPC server in-process with configurable mode and optional tracing. Tests send raw ext-proc request streams and assert on responses without mocking.

### Ginkgo BDD

BDD tests use Ginkgo's Describe/Context/When/It hierarchy. Two import styles coexist:
- Qualified imports (`ginkgo.Describe`) in E2E tests
- Dot-imports (`. "github.com/onsi/ginkgo/v2"`) in sidecar and BDD specs

`DescribeTable` with `Entry` for parameterized behavioral tests (see `pkg/sidecar/proxy/proxy_test.go`).

### testWrapper Lifecycle

E2E tests use `testWrapper()` in `test/e2e/setup_test.go` to inject `BeforeAll`/`AfterAll`/`AfterEach` hooks for namespace creation, Envoy deployment, and cleanup.

### EARS Requirement Annotations

See the EARS Requirements section below.

## EARS Requirements

EARS (Easy Approach to Requirements Syntax) provides structured requirement identifiers embedded in test files. The `test/ears/` package defines the convention.

### Patterns

| Pattern | Template | When to use |
|---------|----------|-------------|
| Ubiquitous | The [system] shall [response] | Always-on behavior |
| Event-Driven | When [trigger], the [system] shall [response] | Triggered behavior |
| Unwanted | If [condition], then the [system] shall [response] | Error/edge cases |
| State-Driven | While [state], the [system] shall [response] | State-dependent behavior |
| Optional | Where [feature], the [system] shall [response] | Feature-gated behavior |

### Convention

Requirements are defined as package-level vars in `test/ears/` with IDs following `{COMPONENT}-{SUBSYSTEM}-{NNN}`:

```go
var SchedNoEndpoints = ears.Req("SCHED-ROUTE-001", ears.PatternEventDriven,
    "When no candidate endpoints exist, the scheduler shall return an error",
    "scheduling")
```

Tests reference requirements via `ears.TestRequirement(t, req)` (standard Go) or `ears.GinkgoRequirement(req)` (Ginkgo):

```go
func TestNoEndpoints(t *testing.T) {
    ears.TestRequirement(t, ears.SchedNoEndpoints)
    // test body
}
```

```go
It("returns an error", func() {
    ears.GinkgoRequirement(ears.SchedNoEndpoints)
    // test body
})
```

### Requirement Files

| File | Component | Count |
|------|-----------|-------|
| `test/ears/scheduling_requirements.go` | Scheduling | 7 |
| `test/ears/flowcontrol_requirements.go` | Flow Control | 9 |
| `test/ears/parsing_requirements.go` | Parsing | 8 |
| `test/ears/plugin_requirements.go` | Plugin Contracts | 6 |

### Generating a Report

```go
import "github.com/llm-d/llm-d-router/test/ears"

ears.GenerateReport(os.Stdout, ears.All())
```

## BDD Test Conventions

### File Naming

BDD spec files are named `*_bdd_test.go` to coexist with existing table-driven `*_test.go` files. Suite bootstraps are `*_bdd_suite_test.go`.

### Package Convention

BDD specs use external test packages (e.g., `package scheduling_test`) to test through the public API.

### Import Style

BDD specs use dot-imports with lint suppression, matching the sidecar convention:

```go
import (
    . "github.com/onsi/ginkgo/v2" //nolint:revive
    . "github.com/onsi/gomega"    //nolint:revive
)
```

### Suite Bootstrap

Each package with BDD specs has a suite bootstrap:

```go
func TestSchedulingBDD(t *testing.T) {
    RegisterFailHandler(Fail)
    RunSpecs(t, "Scheduling BDD Suite")
}
```

### Adding a BDD Spec

1. If the package has no BDD suite, create `*_bdd_suite_test.go` with the bootstrap
2. Create `*_bdd_test.go` with `Describe` > `When` > `It` structure
3. Reference EARS requirements with `ears.GinkgoRequirement()`
4. Run with `go test -v ./path/to/package/...` or `make test-bdd`

## Gherkin Acceptance Tests

### Feature File Convention

Feature files live in `test/acceptance/features/{domain}/` using standard Gherkin syntax:

```gherkin
Feature: Basic request routing
  Background:
    Given a scheduler with default configuration

  Scenario: No available endpoints
    Given 0 endpoints
    When a completion request arrives for model "test-model"
    Then the scheduler returns an error
```

### Step Definitions

Step definitions are in `test/acceptance/*_steps_test.go`. Each domain has its own step file:
- `scheduling_steps_test.go` - Scheduler and routing steps
- `parsing_steps_test.go` - Parser steps
- `flowcontrol_steps_test.go` - Flow control steps
- `common_steps_test.go` - Shared types and initialization

Step functions use `context.Context` for state management between steps.

### Adding an Acceptance Test

1. Create or extend a `.feature` file in `test/acceptance/features/{domain}/`
2. Add step definitions in `test/acceptance/{domain}_steps_test.go`
3. Register steps in `initXxxSteps()` called from `InitializeScenario()`
4. Run with `make test-acceptance`

## Adding New Tests

### Unit Test

1. Create `*_test.go` in the same package as the code under test
2. Use table-driven pattern with `t.Run()`
3. Use `testify/assert` or `testify/require` for assertions
4. Start with nil/empty input cases before functional cases
5. Reference analogous tests in the same package for patterns

### BDD Behavioral Spec

1. Create `*_bdd_suite_test.go` if not already present
2. Create `*_bdd_test.go` with Describe/When/It structure
3. Add EARS requirements to `test/ears/{component}_requirements.go`
4. Reference requirements with `ears.GinkgoRequirement()`

### Integration Test

1. Add to `test/integration/epp/` directory
2. Use the `TestHarness` builder pattern
3. Hermetic tests need no build tag; cluster tests need `integration_tests`
4. Keep tests idempotent and read-only against the cluster

### E2E Test

1. Add scenarios to `test/e2e/e2e_test.go` or create new `*_test.go`
2. Use `ginkgo.When()` with `ginkgo.Ordered` decorator
3. Wrap with `testWrapper()` for lifecycle management
4. Use `ginkgo.By()` for step narration

### Acceptance Test

1. Write `.feature` file in `test/acceptance/features/{domain}/`
2. Add step definitions in `test/acceptance/{domain}_steps_test.go`
3. Use `context.Context` for state between steps
4. Run via `make test-acceptance`

## Test Utilities

| File | Purpose |
|------|---------|
| `test/utils/utils.go` | K8s object CRUD helpers, `TestConfig`, `EventuallyExists`, `PodReady` |
| `test/utils/server.go` | gRPC test server helpers, bufconn-based `LaunchTestGRPCServer` |
| `test/utils/parallel.go` | Port/namespace calculation for parallel Ginkgo processes |
| `test/utils/network.go` | `GetFreePort()` utility |
| `test/utils/dump.go` | `DumpPodsAndLogs` for test failure debugging |
| `pkg/epp/util/testing/wrappers.go` | Test wrapper builders for Pod, InferenceObjective, InferencePool |
| `test/ears/ears.go` | EARS requirement types, `TestRequirement()`, `GinkgoRequirement()` |
| `test/ears/report.go` | EARS requirement Markdown report generator |

## Gaps and Known Limitations

| Gap | Severity |
|-----|----------|
| Prediction server tier not in CI | High |
| Sidecar E2E requires live GPU cluster | High |
| Active-Active HA + prefix routing not tested | Medium |
| Multimodal routing (TTS/STT) no E2E | Medium |
| E2E suite is a single Ginkgo `It` block | Low |
| Flow control benchmarks not in CI | Low |
| `testing.Short()` skips undocumented | Low |

## CI Schedule

| Job | Trigger | Target |
|-----|---------|--------|
| Presubmit | Every PR | `make presubmit` |
| Unit tests | Every PR | `make test-unit` |
| Hermetic integration | Every PR | `make test-integration-hermetic` |
| E2E | Every PR | `make test-e2e` |
| Lint | Every PR | `make lint` |
| Coordinator unit | Every PR | `make -f Makefile.coord.mk test-unit` |
| Coordinator E2E | Every PR | `make -f Makefile.coord.mk test-e2e-coordinator` |
| Cluster integration | On-demand / nightly | `make test-integration` |
| Sidecar E2E | On-demand | `make test-e2e` (sidecar) |
| Nightly perf | Daily 09:00 UTC | GKE + inference-perf |

## Debugging

Run a single test:

```bash
# In builder container (make builder-shell)
go test -v -run TestSchedule ./pkg/epp/scheduling/

# With race detector
go test -v -race -run TestSchedule ./pkg/epp/scheduling/

# With coverage
go test -v -coverprofile=coverage.out ./pkg/epp/scheduling/
go tool cover -html=coverage.out
```

Coverage comparison against a baseline:

```bash
make coverage-compare
```
