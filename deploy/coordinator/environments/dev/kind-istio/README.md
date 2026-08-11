# Coordinator Gateway API environment (multimodal RFC path)

This environment is the linked stack for:

[rh-waterford-et/llm-d-router#10](https://github.com/rh-waterford-et/llm-d-router/pull/10)
(multimodal EPP endpoints) + Gateway API routing + Coordinator.

The Kubernetes objects here (`Gateway`, `HTTPRoute`, `InferencePool`) are
Gateway API / Gateway API Inference Extension — no new CRDs, per the RFC. The
directory is named `kind-istio` because that is the validated **kind**
manifest set (Gateway provider: **Istio**); the same `HTTPRoute`s are reused
as-is on **OpenShift** against the **agentgateway** provider — see
[Running on OpenShift](#running-on-openshift-agentgateway) below.

## Request flow (RFC)

```text
Client
  → Gateway (Istio on kind / agentgateway on OpenShift)   # Kubernetes Gateway API
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

- `Gateway/${GATEWAY_NAME}` — shared front door (`inference-gateway` on kind/Istio, `llm-d-inference-gateway` on OpenShift/agentgateway)
- `HTTPRoute/*-coordinator-route` — catch-all `/` → Coordinator (external RFC paths)
- `HTTPRoute/multimodal-route` — `EPP-Profile: multimodal` → `${MULTIMODAL_POOL_NAME}`
- `HTTPRoute/*-decode-route` — `EPP-Profile: decode` → text InferencePool
- `DestinationRule`s — **Istio/kind only**; no equivalent on agentgateway
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

## Running on OpenShift (agentgateway)

1. Install the Gateway from the llm-d recipe (provider only — not these RFC routes):

   ```bash
   # from a llm-d checkout
   kubectl apply -k guides/recipes/gateway/agentgateway-openshift -n ${NAMESPACE}
   ```

2. Deploy the coordinator stack (Coordinator, text `InferencePool` + EPP,
   multimodal `InferencePool` + EPP, TTS/STT sims) to the same namespace —
   the same components used by `deploy/coordinator/components/`, adjusted for
   OpenShift SCC as `scripts/kubernetes-dev-env.sh` already detects.

3. Apply the Gateway wiring against the OpenShift Gateway:

   ```bash
   PLATFORM=openshift \
     KUBE_CONTEXT=$(oc config current-context) \
     NAMESPACE=${NAMESPACE} \
     ./scripts/apply-gateway-config.sh
   ```

   This applies the same `HTTPRoute`s with `parentRefs.name: llm-d-inference-gateway`,
   **skips** the Istio-only `DestinationRule`s, and points the Coordinator's
   `gateway.address` at the in-cluster address reported in
   `Gateway/llm-d-inference-gateway`'s `status.addresses`.

4. Smoke test against the externally reachable Gateway address (route or
   port-forward):

   ```bash
   PLATFORM=openshift KUBE_CONTEXT=$(oc config current-context) \
     NAMESPACE=${NAMESPACE} GATEWAY_URL=https://<gateway-address> \
     ./scripts/smoke-gateway.sh
   ```

## Note on Envoy AI Gateway

Production llm-d docs also support Envoy AI Gateway / agentgateway recipes under
`llm-d/guides/recipes/gateway/`. The kind manifests in this directory use
**Istio** as the Gateway API implementation — same APIs (`Gateway`, `HTTPRoute`,
`InferencePool`), different controller. Do not install a second GatewayClass
dataplane into a kind cluster running this environment unless you intentionally
replace Istio.
