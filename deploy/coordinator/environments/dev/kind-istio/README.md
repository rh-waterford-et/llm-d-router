# Coordinator Gateway API environment (multimodal RFC path)

This environment is the linked stack for:

[rh-waterford-et/llm-d-router#10](https://github.com/rh-waterford-et/llm-d-router/pull/10)
(multimodal EPP endpoints) + Gateway API routing + Coordinator.

The Kubernetes objects here (`Gateway`, `HTTPRoute`, `InferencePool`) are
Gateway API / Gateway API Inference Extension — no new CRDs, per the RFC. The
directory is named `kind-istio` because that is the validated **kind**
manifest set (Gateway provider: **Istio**); the same `HTTPRoute`s are reused
as-is on **OpenShift**, also against **Istio** — see
[Running on OpenShift](#running-on-openshift-istio) below. (An earlier
`openshift-agentgateway` variant of this environment was removed: it was
never applied against a live cluster. See
`GATEWAY-ARCHITECTURE-KNOWN-RISKS.md`, repo root, item 3.)

## Request flow (RFC)

```text
Client
  → Gateway (Istio, on kind or OpenShift)                  # Kubernetes Gateway API
  → Coordinator                                            # chat pipeline or multimodal passthrough
  → Gateway again (EPP-Profile header)
  → InferencePool + EPP (ext_proc)
  → vLLM / sims (text decode, TTS, STT, …)
```

| Path | Coordinator behaviour | Second hop |
|------|----------------------|------------|
| `/v1/chat/completions` | decode pipeline | `EPP-Profile: decode` → text InferencePool |
| `/v1/audio/speech` | passthrough | `EPP-Profile: multimodal` → TTS pod (`autoregressive-tts`) |
| `/v1/audio/transcriptions` | passthrough | multimodal pool → STT (`encoder-decoder-stt`) |
| `/v1/images/generations` | passthrough | multimodal pool → diffusion (needs a diffusion pod) |
| `/v1/inference` | passthrough | multimodal pool (routing hints parsed by Coordinator/EPP) |

## Gateway objects that matter

- `Gateway/${GATEWAY_NAME}` — shared front door (`inference-gateway` by default on both kind and OpenShift)
- `HTTPRoute/*-coordinator-route` — catch-all `/` → Coordinator (external RFC paths)
- `HTTPRoute/multimodal-route` — `EPP-Profile: multimodal` → `${MULTIMODAL_POOL_NAME}`
- `HTTPRoute/*-decode-route` — `EPP-Profile: decode` → text InferencePool
- `DestinationRule`s — Istio-specific mTLS override, applied on both kind and OpenShift here since both use Istio
- `InferencePool` + EPP Services for text and multimodal

`httproutes.yaml` and `destination-rules.yaml` are templated (`${GATEWAY_NAME}`,
`${POOL_NAME}`, `${MULTIMODAL_POOL_NAME}`, `${EPP_NAME}`, `${MULTIMODAL_EPP_NAME}`)
so the same `HTTPRoute` definitions apply to either platform via
[`scripts/apply-gateway-config.sh`](../../../../../scripts/apply-gateway-config.sh).

## Running on kind (Istio)

```bash
# from llm-d-router repo root (rh-waterford-et/llm-d-router with PR #10)
make -f Makefile.coord.mk image-build
make -f Makefile.coord.mk env-dev-kind-coordinator
```

External entry: `http://localhost:30080`.

Refresh gateway wiring after `env-dev-kind-coordinator`:

```bash
./scripts/apply-gateway-config.sh
```

Verify routing (not model quality):

```bash
./scripts/smoke-gateway.sh
```

Or by hand:

```bash
# Chat (decode pool must resolve)
curl -s http://localhost:30080/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"TinyLlama/TinyLlama-1.1B-Chat-v1.0","messages":[{"role":"user","content":"hello"}],"max_tokens":10}' | jq

# TTS: confirm Coordinator → multimodal EPP → TTS pod (sim may 404; check logs)
curl -s -o /tmp/speech.out -w '%{http_code}\n' http://localhost:30080/v1/audio/speech \
  -H 'Content-Type: application/json' \
  -d '{"model":"tts-1","input":"hello"}'
kubectl logs deploy/vllm-tts-sim --since=30s | grep -a 'audio/speech'

# STT
curl -s -o /tmp/stt.out -w '%{http_code}\n' http://localhost:30080/v1/audio/transcriptions \
  -H 'Content-Type: application/json' \
  -d '{"model":"whisper-1","file":"x"}'
kubectl logs deploy/vllm-stt-sim --since=30s | grep -a 'transcriptions'
```

## Running on OpenShift (Istio)

Two variants exist depending on how much Service/Gateway quota is available:

- **Dedicated Gateway** (this section): a new Gateway/`${GATEWAY_NAME}` object
  is created for this environment. Simpler; use this if quota allows.
- **Shared Gateway, second listener**:
  [`../openshift-istio/`](../openshift-istio/) — for the constrained,
  single-namespace case where an existing Gateway's Service quota is already
  committed elsewhere. That environment is fully self-contained (its own
  kustomization + README) and does not use `apply-gateway-config.sh`.

For the dedicated-Gateway case:

1. Create a Gateway/`${GATEWAY_NAME}` via an Istio-backed `GatewayClass`
   (discover the class name with `oc get gatewayclass` — this is
   cluster-specific, e.g. `data-science-gateway-class` on `wetlab-ai`).

2. Deploy the coordinator stack (Coordinator, text `InferencePool` + EPP,
   multimodal `InferencePool` + EPP, TTS/STT sims) to the same namespace —
   the same components used by `deploy/coordinator/components/`, adjusted for
   OpenShift SCC as `scripts/kubernetes-dev-env.sh` already detects.

3. Apply the Gateway wiring:

   ```bash
   PLATFORM=openshift \
     KUBE_CONTEXT=$(oc config current-context) \
     NAMESPACE=${NAMESPACE} \
     ./scripts/apply-gateway-config.sh
   ```

   This applies the same `HTTPRoute`s and Istio `DestinationRule`s as kind
   (both platforms use Istio here), and points the Coordinator's
   `gateway.address` at the in-cluster address reported in
   `Gateway/${GATEWAY_NAME}`'s `status.addresses`.

4. Smoke test against the externally reachable Gateway address (route or
   port-forward):

   ```bash
   PLATFORM=openshift KUBE_CONTEXT=$(oc config current-context) \
     NAMESPACE=${NAMESPACE} GATEWAY_URL=https://<gateway-address> \
     ./scripts/smoke-gateway.sh
   ```

## Note on other Gateway implementations

Production llm-d docs also support Envoy AI Gateway / agentgateway recipes
under `llm-d/guides/recipes/gateway/`. The manifests in this directory use
**Istio** as the Gateway API implementation on both kind and OpenShift — same
APIs (`Gateway`, `HTTPRoute`, `InferencePool`), different controller. An
agentgateway-based variant of this environment was tried and removed (see
`GATEWAY-ARCHITECTURE-KNOWN-RISKS.md` item 3): the header-vs-path routing
pattern here is asserted to be Gateway-API-generic, but has only actually been
validated against Istio. Do not install a second GatewayClass dataplane into a
kind cluster running this environment unless you intentionally replace Istio.
