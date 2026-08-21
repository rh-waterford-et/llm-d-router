# llm-d Router: Architectural Overview

## Table of Contents

- [Functional Requirements](#functional-requirements)
- [Non-Functional Requirements](#non-functional-requirements)
- [System API and Request Lifecycle](#system-api-and-request-lifecycle)
- [Functional Design](#functional-design)
- [Non-Functional Optimization](#non-functional-optimization)
- [Summary](#summary)

---

## Functional Requirements

What the system must do:

| # | Requirement |
|---|---|
| FR-1 | Accept OpenAI-compatible inference requests (chat, completions, audio, image) |
| FR-2 | Route each request to the pod already holding the KV cache for that prompt prefix |
| FR-3 | Filter pods that cannot serve the request (wrong model, overloaded, wrong modality) |
| FR-4 | Score surviving pods and pick the winner |
| FR-5 | Support disaggregated inference: route Prefill and Decode to separate pods |
| FR-6 | Support multimodal disaggregation: Encode on dedicated workers, then Prefill, then Decode |
| FR-7 | Continuously collect pod-level metrics (KV cache utilization, loaded adapters, queue depth) |
| FR-8 | Allow operators to extend routing logic without changing core code (plugin model) |
| FR-9 | Manage request priority and fairness (FCFS, strict fairness, utilization limits) |
| FR-10 | Support model name rewriting for A/B testing and canary rollouts |

What it does **not** do: serve model weights, run inference, manage GPU memory, or act as a Kubernetes scheduler.

---

## Non-Functional Requirements

| Attribute | Concern | Current Trade-off |
|---|---|---|
| **Performance / Latency** | TTFT (Time-to-First-Token) is the primary SLA signal | KV-cache-aware routing achieves 57x faster TTFT vs round-robin on cache-warm workloads |
| **Throughput** | Requests per second across the cluster | ~2x throughput uplift on identical hardware vs naive load balancing |
| **Scalability** | EPP CPU scales linearly with request rate; memory with concurrent tokens | Active-Active HA gives near-linear throughput scaling except when prefix-cache routing is active — partitioned state degrades hit rates |
| **Availability** | Active-Passive HA for prefix-cache mode; Active-Active for throughput mode | Single point of failure risk in Active-Passive; cache degradation risk in Active-Active |
| **Extensibility** | New routing strategies without forking core | Plugin model: Filters, Scorers, and Scrapers are all hot-swappable via config |
| **Observability** | Operators need to see routing decisions in real time | EPP emits structured logs per request; Prometheus metrics exported |

---

## System API and Request Lifecycle

The system exposes a single surface: OpenAI-compatible HTTP endpoints.

```
Client
  |  POST /v1/chat/completions  (or /audio/speech, /audio/transcriptions, etc.)
  v
Envoy Proxy  (L7, FULL_DUPLEX_STREAMED ext-proc)
  |  ext-proc gRPC call  ->  request headers + body
  v
EPP -- Endpoint Picker  ("the brain")
  |
  +-- 1. PARSE      RequestHandler parses format (OpenAI / Anthropic / vLLM)
  |                 extracts model name, prompt, modality, priority
  |
  +-- 2. FLOW CTL   FlowControl admits or queues the request
  |                 (FCFS ordering -> fairness policy -> utilization gate)
  |
  +-- 3. DATA       DataLayer reads live pod metrics from shared datastore
  |                 (populated by background scrapers polling /metrics)
  |
  +-- 4. FILTER     Sequential filter chain eliminates ineligible pods
  |                 e.g. decode-filter, modality-filter, capacity-filter
  |
  +-- 5. SCORE      Weighted scorers rank surviving pods
  |                 e.g. prefix-cache-scorer (weight 50), least-queued scorer
  |
  +-- 6. PICK       max-score-picker selects the winning pod
  |                 (random tiebreak on equal scores)
  |
  +-- 7. INJECT     EPP injects routing headers back into Envoy
                    Envoy forwards the request to the winning pod
  v
vLLM Pod  (Decode Worker)
  |  [if P/D disaggregation active]
  +-- Sidecar reads x-prefiller-host-port header
  +-- Sends prefill work to Prefill Worker
  +-- Runs decode locally -> streams tokens back
  v
Client  (SSE token stream or JSON response)
```

**Background loop (concurrent, not on the request path):**

```
MetricScraper polls pods every N seconds
  -> DataSource collects raw metrics
  -> Extractor populates shared Datastore
  -> Scorers read fresh state on next request
```

---

## Functional Design

**Architectural style:** Event-driven sidecar + plugin pipeline on Kubernetes.

### The Four Layers

```
+-----------------------------------------------------+
|  PROXY LAYER                                        |
|  Envoy (or Istio / GCloud ALB)                      |
|  -- L7 HTTP, ext-proc protocol to EPP               |
+-------------------+---------------------------------+
                    | ext-proc gRPC
+-------------------v---------------------------------+
|  ROUTING LAYER  (EPP)                               |
|  RequestHandler -> FlowControl -> Scheduler         |
|  Plugin pipeline: Filters -> Scorers -> Picker      |
+-------------------+---------------------------------+
                    | shared datastore reads
+-------------------v---------------------------------+
|  DATA LAYER                                         |
|  Scrapers -> DataSources -> Extractors -> Datastore |
|  (KV cache usage, queue depth, loaded adapters)     |
+-------------------+---------------------------------+
                    | Kubernetes pod discovery
+-------------------v---------------------------------+
|  COMPUTE LAYER                                      |
|  InferencePool of vLLM pods                         |
|  (optionally: dedicated Prefill / Encode pods)      |
+-----------------------------------------------------+
```

### Key Design Patterns

| Pattern | Where used | Why |
|---|---|---|
| **Plugin Pipeline** | Filter chain + Scorer chain | Open/Closed Principle: add routing logic without touching core |
| **CQRS** | DataLayer: scrapers write to datastore; scorers read from it | Write path (scraping) and read path (scoring) are optimized independently |
| **Materialized View** | Shared Datastore | Precomputed pod state so scoring is O(1) lookup, not live polling per request |
| **Sidecar** | pd-sidecar on Decode pods | Orchestrates P/D handshake without changing vLLM itself |
| **CRD-driven config** | InferencePool, InferenceModel, InferenceObjective | Kubernetes-native: operators use kubectl, no custom API servers |
| **Profile Handler** | SchedulingProfiles | Single EPP supports both standard and disaggregated routing by switching profile per request |

### Data Types in Flight

- **Request context:** model name, prompt tokens, modality (text/audio/image), priority class
- **Pod metadata:** model loaded, LoRA adapters, KV-cache blocks occupied, queue depth
- **Routing decision:** winning pod IP:Port, injected headers (`x-prefiller-host-port`, `x-encoder-hosts-ports`)

---

## Non-Functional Optimization

Where the system gets hard, and the trade-offs operators must understand:

| Bottleneck | Mechanism | Trade-off |
|---|---|---|
| **Prefix-cache partitioning** | Active-Active HA splits prefix state across replicas | Use Active-Passive when prefix-cache-scorer is active; accept a lower throughput ceiling |
| **EPP CPU at scale** | `maxPrefixBlocksToMatch` controls the CPU/accuracy trade-off | Default 256 blocks = low CPU; 6250 blocks = high hit rate but EPP CPU can exceed 1 core per req/s |
| **Head-of-line blocking on long decode** | Chunked Decode (experimental) splits decode into token-budget chunks | Improves average TTFT for all requests; adds coordination overhead |
| **Single InferencePool per Envoy** | Current ext-proc architecture limitation | Multi-model clusters require multiple InferencePools and multiple Envoy deployments |
| **Metric staleness** | Scraper polling interval | Stale data means scorers can route to a pod that just became saturated; the polling interval is a latency/accuracy trade-off |

---

## Summary

The system is a **smart L7 proxy brain** that sits between clients and LLM pods. Its core value is the **Materialized View + CQRS split**: background scrapers continuously update a shared datastore of pod state; the request path reads that state in microseconds, runs a sequential filter-score-pick pipeline, and injects the routing decision back into Envoy — all without talking to pods directly during serving.

The architecture's central tension is that **KV-cache locality and horizontal scaling are in conflict**: the more replicas of EPP you run for throughput, the more the prefix state gets partitioned, and the worse cache hit rates become. Every scaling decision in this system flows from that trade-off.

---

## References

- [Architecture](architecture.md) — routing flow and plugin configuration detail
- [Disaggregation](disaggregation.md) — P/D and E/P/D lifecycle
- [Operations](operations.md) — EPP container sizing guide
- [Plugin README](../pkg/epp/framework/plugins/README.md) — available filters, scorers, and data producers
