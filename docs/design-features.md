# Design Features in Practice

_How the system-design-methodology maps to the llm-d-router implementation_

---

## What llm-d-router Is

llm-d-router is the intelligent routing layer for LLM inference traffic on Kubernetes. It consists of three binaries: the **Endpoint Picker (EPP)**, the **Disaggregation Sidecar** (pd-sidecar), and the experimental **Coordinator**. Together they handle request classification, endpoint selection, multi-stage inference orchestration, flow control, and KV cache tracking — everything between a client sending a completion, audio, or image generation request and a GPU executing it. The Coordinator serves two request classes: chat and completions requests that run through the configurable step pipeline, and audio, image, and embedding requests that are passed through directly to the Inference Gateway and routed by pod architecture label without pipeline processing.

---

## 1. Functional Requirements → What the System Does

| Action | Component | How it's expressed |
|---|---|---|
| Accept an inference request | Envoy Proxy / Coordinator | OpenAI-compatible HTTP (`/v1/completions`, `/v1/chat/completions`, `/v1/audio/speech`, `/v1/audio/transcriptions`, `/v1/images/generations`, `/v1/embeddings`) |
| Select the optimal model server pod | EPP (`pkg/epp/scheduling/`) | Filter → Score → Pick cycle per request |
| Enforce tenant priority and fairness | EPP (`pkg/epp/flowcontrol/`) | Per-band priority queues with pluggable fairness and ordering policies |
| Orchestrate P/D disaggregated inference | pd-sidecar (`pkg/sidecar/`) | Coordinates prefill worker, pulls KV transfer params, forwards to local decoder |
| Orchestrate E/P/D multimodal inference | pd-sidecar + Coordinator (`pkg/coordinator/`) | Encode → Prefill → Decode pipeline; coordinator drives one EPP call per phase tagged `EPP-Profile: encode \| prefill \| decode`; a single EPP with `header-profile-handler` selects the matching scheduling profile |
| Route TTS, STT, and image generation requests | Coordinator passthrough + EPP modality filter (`pkg/epp/framework/plugins/scheduling/filter/modality/`) | `llm-d.ai/model-arch` pod label maps request path to compatible architectures (`autoregressive-tts`, `encoder-decoder-stt`, `diffusion`, `omni-llm`) |
| Track KV cache state across the pool | KV Events + KV Cache (`pkg/kvevents/`, `pkg/kvcache/`) | ZMQ event subscription; radix-tree block index |
| Discover endpoints without Kubernetes | Discovery plugin (`pkg/epp/datalayer/`) | File-based or custom `EndpointDiscovery` interface |
| Rewrite model names for A/B or canary | `InferenceModelRewrite` API | EPP header mutation before forwarding to model server |
| Emit routing and pool health metrics | EPP metrics (`pkg/epp/metrics/`) | Prometheus on `:9090/metrics` |

**Explicitly out of scope** for this repo: executing the model, managing GPU memory within a pod, implementing the tokenizer (except as input to KV block key derivation in `pkg/kvcache/tokenization/`), acting as a Kubernetes scheduler.

---

## 2. Non-Functional Requirements → The Constraints That Shape Everything

> **Central tension:** KV-cache locality and horizontal scaling are in conflict. The more EPP replicas you run for throughput, the more prefix state gets partitioned across instances, and the worse cache hit rates become. Every scaling decision in this system flows from that trade-off.

### Scalability

**Entry-point horizontal scaling** — The EPP is stateless on the request path. The controller manager syncs Kubernetes objects (`InferencePool`, `InferenceObjective`, `InferenceModelRewrite`) into an in-process datastore (`pkg/epp/datastore/`). Multiple EPP replicas can run in active-active mode when the KV Indexer uses pod-discovery ZMQ delivery so every replica independently sees the full KV event stream.

**API gateway responsibilities** — The EPP is the single point where rate limiting (`flowControl.maxRequests`, `maxBytes`), tenant identification (`x-llm-d-inference-fairness-id`), priority assignment (`InferenceObjective`), and model name rewriting (`InferenceModelRewrite`) are enforced. Individual model servers need none of this logic.

**Pluggable data layer** — The Source → Extract → Attribute pipeline (`pkg/epp/framework/plugins/datalayer/`) scrapes per-endpoint metrics (queue depth, KV cache utilisation, active adapters) and feeds them into a shared datastore. Adding a new signal source requires no changes to the scheduler — only a new data-source or extractor plugin.

**Non-Kubernetes deployment** — The discovery plugin interface (`docs/discovery.md`) allows the EPP to run without Kubernetes at all. A `file-discovery` plugin reads endpoint lists from a JSON/YAML file with live-reload, enabling bare-metal or Slurm deployments with identical scheduling logic.

**Coordinator statelessness** — The Coordinator (`pkg/coordinator/`) is explicitly designed to be stateless: all per-request state lives on a `RequestContext` that dies with the request. Any replica can serve any request; horizontal scaling is a load balancer in front of N identical instances.

### Availability

**FailOpen / FailClose** — The `InferencePool` `failureMode` controls what Envoy does when the EPP is unreachable. `FailOpen` (default in production deployments) routes traffic directly to a model server pod without scheduling intelligence. The request is served rather than dropped; flow control and priority policies are bypassed for that request only.

**Active-active EPP** — Pod-discovery ZMQ mode (`pkg/kvevents/pool.go`) has every EPP replica independently subscribe to every model server's KV event socket. All replicas converge on identical index state. There is no primary/replica relationship and no shared mutable state between EPP instances. Active-Active is the right choice for throughput-first deployments; when prefix-cache-scorer is active, use Active-Passive instead — partitioned prefix state in Active-Active degrades cache hit rates, and the single-point-of-failure risk of Active-Passive is the accepted trade-off.

**Graceful degradation in scheduling** — If the latency predictor sidecar is unreachable, the `predicted-latency-producer` falls back to composite heuristic scoring (KV cache utilisation + queue depth + prefix match) automatically. No operator action required; traffic continues with reduced routing precision.

**Graceful drain** — On EPP shutdown, flow-controlled queued requests are evicted with HTTP 503 (`rejected-shutting-down`), signalling clients and upstream gateways to retry rather than treat the response as a hard fault. The `x-llm-d-request-dropped-reason` header identifies the exact drop cause for client-side retry logic.

**Sidecar fault tolerance** — In P/D disaggregation, if the prefill worker returns a 5xx error, the pd-sidecar falls back to decoder-only execution automatically. Client errors (4xx) are not retried (they indicate a request-level problem, not infrastructure).

### Performance

**KV cache affinity as the primary performance lever** — The `prefix-cache-scorer` (`pkg/epp/framework/plugins/scheduling/scorer/prefix/`) and `precise-prefix-cache-producer` score endpoints by how much of the request's prompt prefix they already hold in cache. Routing to a pod with a cache hit avoids recomputing the prefill phase — the most FLOPs-intensive operation. Measured: 2× faster TTFT and 3× higher throughput vs round-robin.

**Two precision levels for prefix matching:**
- **Approximate** (`approx-prefix-cache-producer`): heuristic radix-tree over scheduling history. Low overhead, works without ZMQ.
- **Precise** (`precise-prefix-cache-producer` + KV Indexer): event-driven `block key → pod` map updated by `KVEvents` over ZMQ from model servers. Handles multimodal models, hybrid-attention models, and advanced eviction policies where byte-based heuristics break down.

**Late-binding scheduling** — Flow control holds requests in the EPP until backend capacity is available, then runs the scheduling cycle at the last possible moment. This means the routing decision uses the freshest possible endpoint state (queue depth, KV cache utilisation), not state from when the request arrived.

**Chunked decode** — The pd-sidecar supports `--decode-chunk-size`, splitting long decode runs into shorter chunks. Each chunk respects the KV cache block size for efficient memory use. This reduces average TTFT for all requests by preventing a single long request from blocking others through the full decode phase, at the cost of added coordination overhead per chunk boundary.

**SLO-aware routing** — The `latency-scorer` and `slo-headroom-tier-filter` route based on predicted latency headroom (gap between predicted TTFT/TPOT and the `x-llm-d-slo-ttft-ms` header value). Endpoints without headroom are filtered or deprioritised before selection, making latency budgets a first-class routing constraint rather than a post-hoc concern.

---

## 3. API and Sequence Design → Contracts First

### Call Signatures

```
# Client-facing (OpenAI-compatible)
# Sidecar model: Envoy → EPP (ext_proc) → model server
# Coordinator model: Inference Gateway → Coordinator → pipeline or passthrough → EPP → pod
POST /v1/completions               → { id, choices[], usage }   # sidecar + coordinator pipeline
POST /v1/chat/completions          → { id, choices[], usage }   # sidecar + coordinator pipeline
POST /v1/audio/speech              → audio binary               # coordinator passthrough only
POST /v1/audio/transcriptions      → { text }                   # coordinator passthrough only
POST /v1/images/generations        → { created, data[] }        # coordinator passthrough only
POST /v1/embeddings                → { object, data[], usage }  # coordinator passthrough only

# EPP ↔ Proxy (internal)
ext_proc (gRPC, FULL_DUPLEX_STREAMED)  → endpoint address + mutated headers

# Sidecar ↔ Workers (internal, HTTP)
  x-prefiller-host-port header     → triggers P/D path in sidecar
  x-encoder-hosts-ports header     → triggers E stage in sidecar
  x-kv-cache-source-host-port      → triggers P2P cached-prefix pull

# Model servers → EPP (event-driven, ZMQ PUB/SUB)
KVEvent { BlockStored | BlockRemoved | AllBlocksCleared }
```

### Key Sequence: EPP Request Pipeline

```
Client
  |  POST /v1/chat/completions  (or /audio/speech, /audio/transcriptions, etc.)
  v
Envoy Proxy  (L7, FULL_DUPLEX_STREAMED ext-proc)
  |  ext-proc gRPC call  ->  request headers + body
  v
EPP
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
  |                 e.g. decode-filter, modality-filter, utilization-filter
  |
  +-- 5. SCORE      Weighted scorers rank surviving pods
  |                 e.g. prefix-cache-scorer (weight 50), least-queued scorer
  |
  +-- 6. PICK       max-score-picker selects the winning pod
  |                 (random tiebreak on equal scores)
  |
  +-- 7. INJECT     EPP injects routing headers back into Envoy
  |                 Envoy forwards the request to the winning pod
  v
vLLM Pod
  |  [if P/D disaggregation active]
  +-- Sidecar reads x-prefiller-host-port header
  +-- Sends prefill work to Prefill Worker
  +-- Runs decode locally -> streams tokens back  (SSE or JSON)
  v
Client
```

**Background loop (concurrent, not on the request path):**

```
MetricScraper polls pods every N seconds
  -> DataSource collects raw metrics
  -> Extractor populates shared Datastore
  -> Scorers read fresh state on next request
```

### Key Sequence: P/D Disaggregated Request

```
Client → Envoy → EPP (disagg-profile-handler selects D + optionally P)
       → pd-sidecar on Decode pod
           → Prefill worker  (max_tokens=1, captures KVTransferParams)
           ← KVTransferParams
           → Decode worker   (do_remote_prefill=true, NIXL RDMA pulls KV)
           ← tokens
       ← Envoy ← response
```

This sequence drove: the pd-sidecar requirement on every decode pod, the `x-prefiller-host-port` header as the EPP→sidecar handoff, the `prefix-based-pd-decider` to gate whether disaggregation is worth the network cost, and NIXL RDMA as a hard infrastructure dependency.

### Key Sequence: Flow Control Dispatch

```
Request arrives → FlowKey assigned (FairnessID + Priority)
               → enqueued in per-band priority queue
               → Saturation Detector polled on each dispatch cycle
               → [gate open] → late-binding Scheduler → endpoint selected → forwarded
               → [gate closed] → head-of-line block until capacity frees
```

The "park in EPP rather than in model server local queue" design is what enables priority reordering, fairness enforcement, and late-binding cache affinity — none of which are possible once a request is committed to a specific model server's queue.

### Key Sequence: Multimodal Passthrough (TTS / STT / Image Generation)

```
Client → Inference Gateway → Coordinator (handlePassthrough, no EPP-Profile header)
       → Inference Gateway → EPP (modality-filter checks llm-d.ai/model-arch label)
       → architecture-matched pod (e.g., autoregressive-tts, diffusion, encoder-decoder-stt)
       ← audio binary / image data / transcript JSON
       ← Coordinator → Client
```

`handlePassthrough` is an `httputil.ReverseProxy` that forwards the inbound request to the Inference Gateway verbatim — no body parsing, no `RequestContext`, no pipeline steps, no `EPP-Profile` header. `Content-Type` is preserved as-is (critical for multipart `audio/transcriptions` requests). The response, including binary audio, is streamed back to the client unchanged via `FlushInterval: -1`. The Coordinator's only contribution on this path is request-ID assignment and error logging; all routing intelligence sits in the EPP.

The EPP's `modality-filter` (`pkg/epp/framework/plugins/scheduling/filter/modality/`) drops pods whose `llm-d.ai/model-arch` label is incompatible with the request path: `/v1/audio/speech` accepts `omni-llm` or `autoregressive-tts`; `/v1/audio/transcriptions` accepts `encoder-decoder-stt`; `/v1/images/generations` accepts `diffusion`. Embeddings (`/v1/embeddings`) pass through without modality filtering.

---

## 4. Architecture Style → Microservices + Event-Driven, Composed by Layer

**Architectural style:** Event-driven sidecar + plugin pipeline on Kubernetes.

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

| Layer | Style | Where in the code |
|---|---|---|
| Request routing | Microservice (EPP) | `pkg/epp/` — gRPC ext_proc server, independent process |
| Disaggregation orchestration | Sidecar microservice | `pkg/sidecar/` — one per decode pod, HTTP to workers |
| Full-pipeline orchestration + passthrough | Stateless microservice | `pkg/coordinator/` — step-based pipeline for chat/completions; direct passthrough for audio, image, and embedding requests |
| KV cache state tracking | Event-driven | `pkg/kvevents/` — ZMQ SUB consumer; `pkg/kvcache/` — index |
| Endpoint metric collection | Poll + push | `pkg/epp/framework/plugins/datalayer/` — scrapes `/metrics` per pod |
| Kubernetes object sync | Kubernetes controller | `pkg/epp/controller/` — watches InferencePool, InferenceObjective |

### Binaries and Their Responsibilities

| Binary (`cmd/`) | Package | Role |
|---|---|---|
| `epp` | `pkg/epp/` | Routing intelligence: scheduling, flow control, KV indexing, metrics |
| `pd-sidecar` | `pkg/sidecar/` | Per-request orchestration: P/D/E phase coordination, KV protocol translation |
| `coordinator` | `pkg/coordinator/` | Two request classes: step-pipeline orchestration (download → tokenize → encode → prefill → decode) for chat and completions; direct passthrough to the Inference Gateway for audio, image, and embedding requests |

### Data Types and Stores

| Data | Store | Rationale |
|---|---|---|
| Endpoint state (queue depth, KV utilisation, active adapters) | In-process datastore (`pkg/epp/datastore/`) | Sub-millisecond read on scoring hot path |
| KV block → pod index (precise mode) | Radix-tree in `pkg/kvcache/` | O(prefix length) lookup; updated by ZMQ events |
| Flow control queues | In-process memory (`pkg/epp/flowcontrol/`) | Low latency; intentionally non-durable |
| Kubernetes objects (InferencePool, InferenceObjective) | Kubernetes API → controller cache | Source of truth for pool membership and policy |
| Endpoint list (non-Kubernetes mode) | File on disk (JSON/YAML, live-reload) | Enables bare-metal and Slurm deployments |
| Prometheus metrics | Time-series (`:9090/metrics`) | Autoscaling via KEDA; operator dashboards |

### Protocols Between Components

| Pair | Protocol |
|---|---|
| Client → Envoy | HTTP/1.1 or HTTP/2 (OpenAI-compatible) |
| Envoy → EPP | gRPC `ext_proc` (`FULL_DUPLEX_STREAMED`) |
| Envoy → Model server | HTTP (forwarded after EPP selects endpoint) |
| EPP → Model server metrics | HTTP scrape (`/metrics` per pod) |
| Model server → EPP KV Indexer | ZMQ PUB/SUB (`BlockStored`, `BlockRemoved`, `AllBlocksCleared`) |
| pd-sidecar → Prefill worker | HTTP with `kv_transfer_params` mutation |
| pd-sidecar → Encode worker | HTTP with multimodal content |
| Decode worker → Prefill/Encode worker | NIXL RDMA (KV pull), or shared storage, or Mooncake |
| Client → Coordinator | HTTP/1.1 or HTTP/2 (OpenAI-compatible) |
| Coordinator → Inference Gateway (per phase) | HTTP with `EPP-Profile: encode \| prefill \| decode`; `header-profile-handler` selects the matching scheduling profile from a single EPP instance |

---

## 5. Optimisation Pass → Every NFR Addressed

### Eliminate Single Points of Failure

| Risk | Mitigation | Where |
|---|---|---|
| EPP is the only routing brain | `FailOpen` degrades to unprotected pass-through; multi-EPP active-active via pod-discovery ZMQ | `pkg/kvevents/pool.go`, InferencePool `failureMode` |
| Latency predictor unavailable | Automatic fallback to composite heuristic scoring | `predicted-latency-producer` plugin |
| Prefill worker 5xx during disaggregation | pd-sidecar falls back to decoder-only execution | `pkg/sidecar/proxy/` |
| EPP process restart | 503 + `rejected-shutting-down` header on graceful drain; clients retry | `pkg/epp/flowcontrol/` |
| Model server pod removed | Controller watch evicts pod from datastore before next scheduling cycle | `pkg/epp/controller/` |
| Endpoint overloaded but not yet down | `utilization-filter` hard-caps any combination of EPP-tracked active requests, model-server running requests, waiting queue size, and KV cache utilization; `fallbackOnEmpty` preserves routability when all endpoints breach a threshold | `pkg/epp/framework/plugins/scheduling/filter/utilization/` |

### Eliminate Bottlenecks

**The GPU KV cache is the fundamental bottleneck.** llm-d-router attacks it at three levels:

1. **Reuse it** — prefix-cache-aware routing routes to the pod that already computed the matching prefill, avoiding redundant FLOPs (`prefix-cache-scorer`, `precise-prefix-cache-producer`)
2. **Specialise for it** — disaggregated P/D separates the FLOPs-bound prefill phase from the memory-bandwidth-bound decode phase onto independently sized worker pools, removing the "long prefill blocks decodes" interference pattern (`disagg-profile-handler`, pd-sidecar)
3. **Protect it** — flow control queues requests in the EPP rather than in the model server's local queue, preventing KV cache thrashing from concurrent context switches. The saturation detector gates dispatch until backend KV capacity is available (`pkg/epp/flowcontrol/`)

**Decode head-of-line blocking** — Chunked decode (`--decode-chunk-size`) caps each token-generation pass at a configurable budget, interleaving long and short requests at chunk boundaries. Token budget should be a multiple of the KV cache block size.

**Prefix matching CPU cost** — `maxPrefixBlocksToMatch` controls the CPU/accuracy trade-off for prefix cache scoring. The default (256 blocks) keeps EPP CPU low. At 6250 blocks, hit rates improve significantly but EPP CPU can exceed 1 core per req/s — size EPP replicas accordingly when raising this value.

**Multi-model topology constraint** — A single Envoy deployment supports one `InferencePool` (current ext-proc architecture limitation). Multi-model clusters require multiple `InferencePool` objects and a separate Envoy deployment per pool.

**Autoscaling signal quality** — Traditional GPU utilisation lags and is non-linear. The EPP's flow-control queue depth is a definitive "true demand" signal: requests waiting in queue = capacity that doesn't exist yet. KEDA reads this via Prometheus for proactive scale-out before the pool saturates.

### Optimise Critical Paths

**TTFT (Time To First Token):** Prefix cache hit → skip prefill recomputation. If no hit: disaggregated prefill on dedicated compute → latency predictor SLO headroom scoring picks the pod most likely to meet the budget.

**TPOT (Time Per Output Token):** Flow control prevents KV cache thrashing. By holding requests in EPP queues and only dispatching when backend KV capacity is confirmed available, each dispatched request runs decode without being interrupted by prefill from a later request.

### Algorithms and Data Structures Matched to Access Patterns

| Access pattern | Structure / Algorithm | Package |
|---|---|---|
| Prefix match for cache affinity (heuristic) | Radix/trie tree over token-block hashes | `pkg/kvcache/` |
| Precise block-level cache lookup | `block key → pods` hash map, updated by ZMQ events | `pkg/kvcache/indexer.go`, `pkg/kvevents/` |
| Endpoint scoring | Weighted sum of plugin scores (0.0–1.0), each capped and combined | `pkg/epp/scheduling/weighted_scorer.go` |
| Priority dispatch | Strict band ordering; round-robin within band | `pkg/epp/flowcontrol/` |
| Request ordering within flow | FCFS list (default), EDF min-heap, SLO-deadline heap | `pkg/epp/framework/plugins/flowcontrol/ordering/` |
| Latency prediction | XGBoost regression with stratified bucketing; ~5% | Sidecar to EPP via HTTP |
| Event deduplication | Dedup filter before indexing | `pkg/kvevents/event_dedup_filter.go` |
| Endpoint discovery | Kubernetes watch (default) or file watch (non-K8s) | `pkg/epp/controller/`, file-discovery plugin |

---

## Patterns in Action

### CQRS — Applied to KV Cache State

The KV event subsystem (`pkg/kvevents/` + `pkg/kvcache/`) is a direct CQRS implementation:

- **Write path (Commands):** Model servers publish `KVEvents` — `BlockStored`, `BlockRemoved`, `AllBlocksCleared` — over ZMQ whenever their cache changes. `pkg/kvevents/zmq_subscriber.go` subscribes and pipes events through the dedup filter into the indexer. These are the commands that mutate global state.
- **Read path (Queries):** During the Filter → Score → Pick cycle, `precise-prefix-cache-producer` queries `pkg/kvcache/indexer.go` for the `block key → pod` mapping. This read path is fully decoupled from and never blocked by the write path.
- **Eventual consistency:** The index is near-real-time. A block evicted between two ZMQ events may transiently appear as resident. The scheduler handles this gracefully: a "stale hit" routing decision falls back to local prefill on the selected pod, the same outcome as a cold start.
- **Metric staleness is a separate concern:** The KV block index is event-driven and near-real-time. Pod utilization metrics (queue depth, running requests) are scraper-polled on an interval. Stale utilization data can cause scorers to route to a pod that just became saturated; the polling interval is a latency/accuracy trade-off operators tune per deployment.

### Materialised View — The KV Block Index

`pkg/kvcache/indexer.go` is a materialised view of distributed KV cache state:

- **What it pre-computes:** Which content-addressed cache blocks are resident on which pods, across HBM and offload tiers.
- **How it stays current:** Continuously, event-driven — `BlockStored` and `BlockRemoved` events update the index in real time. `AllBlocksCleared` (triggered by, e.g., RL weight rollouts) drops all entries for that pod instantly.
- **What it saves:** Polling every pod on every incoming request — O(pods × requests) network calls reduced to O(1) local lookups. The EPP never contacts pods directly during serving; all routing decisions are made from pre-populated datastore state.
- **Trade-off:** Write amplification (every KV event is processed and stored in the EPP index) in exchange for sub-millisecond routing decisions on the scoring hot path.
- **Pairs with CQRS:** The index is the query-side projection. `KVEvents` over ZMQ is the command side. Both sides evolve independently: new event types or index structures can be added without changing the other.

### Plugin Pipeline — Open/Closed at the Scheduling Layer

The scheduler (`pkg/epp/scheduling/scheduler.go`) follows the **open/closed principle**: closed to modification, open to extension via plugins. Adding a new scoring signal, filter criterion, or picking strategy requires only:

1. Implement the `Filter`, `Scorer`, or `Picker` interface in `pkg/epp/framework/`
2. Register it in the plugin registry
3. Reference it in `EndpointPickerConfig` YAML

No changes to the scheduler, profile handler, or any existing plugin. This is enforced by design: `pkg/epp/framework/plugins/README.md` explicitly states "No core changes are needed to add new scorers or filters."

The same pattern applies to flow control extension points (`FairnessPolicy`, `OrderingPolicy`, `SaturationDetector`) and data layer components (`DataSource`, `MetricsExtractor`).

Recent additions illustrate the pattern:
- `utilization-filter` (`pkg/epp/framework/plugins/scheduling/filter/utilization/`) — a single plugin that hard-caps any combination of active requests, running requests, waiting queue size, and KV cache utilization. Conditions combine with OR semantics: an endpoint is dropped as soon as any metric exceeds its cap. Replaces and generalizes the earlier `active-request-filter` with no changes to the scheduler.
- `header-profile-handler` (`pkg/epp/framework/plugins/scheduling/profilehandler/headerprofile/`) — a `ProfileHandler` that runs the scheduling profile named by the `EPP-Profile` request header. Enables one EPP instance to cover all three coordinator phases (encode, prefill, decode) from a single `EndpointPickerConfig`, replacing the earlier three-EPP-instance deployment.
- `modality-filter` (`pkg/epp/framework/plugins/scheduling/filter/modality/`) — filters pods by `llm-d.ai/model-arch` label based on the request path, routing audio and image requests to architecturally compatible pods without any change to the core scheduler.
