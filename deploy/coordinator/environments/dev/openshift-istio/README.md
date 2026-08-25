# Coordinator OpenShift/Istio environment (real, tested)

The validated, kustomize-owned replacement for the old `../openshift-agentgateway/`
stub (removed - it was never applied to a live cluster), and a deliberately
separate environment from `../openshift-minimal/` (the wetlab demo, which
hand-patches Helm-managed objects and uses one shared InferencePool against
real GPU-backed models). This environment reuses the exact same shared
building blocks as `../kind-istio/` - two InferencePools + EPPs, decode/TTS/STT
simulator workers, shared RBAC - and was validated live against the
`wetlab-ai` OpenShift cluster's real `data-science-gateway-class` Istio
Gateway implementation.

See `GATEWAY-ARCHITECTURE-KNOWN-RISKS.md` (repo root) item 3 for why the
agentgateway path was retired in favor of this one, and item 5 for why this is
kept separate from `openshift-minimal`.

## Prerequisites (learned the hard way - see known-risks doc item 9)

Before applying anything here, the cluster/namespace needs:

- The Gateway API CRDs (`gateways.gateway.networking.k8s.io`,
  `httproutes.gateway.networking.k8s.io`) and the Gateway API Inference
  Extension CRD (`inferencepools.inference.networking.k8s.io`) installed.
- RBAC allowing your account to create/patch `Gateway`, `HTTPRoute`,
  `InferencePool` objects in the target namespace.
- A `GatewayClass` backed by an Istio-based controller already installed
  cluster-wide (this was `data-science-gateway-class` on `wetlab-ai`; the
  name is cluster-specific, discover it with `oc get gatewayclass`).
- If reusing an existing shared Gateway (as this environment does - see
  below): that Gateway and its mesh must already have working mTLS between
  the Gateway and any EPP it talks to. We hit a real Gateway<->EPP mTLS
  mismatch on this exact cluster that needed a platform-level fix (mesh
  membership); there is no client-side workaround for it.
- Enough free quota for 3 new `Service` objects (one Coordinator, two EPPs)
  plus 6 new pods (Coordinator, 2 EPPs, 3 sim workers) at the namespace's
  `LimitRange` defaults. Check with `oc get resourcequota,limitrange`
  before applying - we hit a fully-exhausted `services` quota building this
  and had to free it by deleting orphaned Services first.

## Why this shares a Gateway instead of creating its own

Real constraint hit building this: the target namespace's `Service` quota
was already fully committed to the live demo (`openshift-minimal`) when
this was built, room for only 3 more. Creating a second `Gateway` object
would need a 4th (Istio auto-creates one Service per Gateway). So instead
of a new Gateway, this environment adds a **second listener** to the
existing shared Gateway (see `gateway-listener-patch.yaml`) and scopes all
three of its `HTTPRoute`s to that listener via `parentRefs.sectionName`.

This is not a compromise on isolation: a `sectionName`-scoped `HTTPRoute` is
only evaluated against traffic on that specific listener/port. Even though
this environment's Coordinator stamps the exact same `EPP-Profile:
decode`/`multimodal` header values as the live demo (those are Go
constants, not configurable per-environment), traffic on the two listeners
never crosses - this environment's Coordinator points `gateway.address` at
the new listener's own port, not port 80, so its second-hop requests are
only ever evaluated against this environment's own routes.

If you have full quota room (a dedicated namespace, or a cluster without
this constraint), prefer a real second `Gateway` object instead - it's
simpler to reason about and matches `kind-istio`'s pattern exactly. This
listener-sharing approach is the one-namespace-constrained fallback, not
the preferred design; it's documented here as a real, tested example of
what to do when you don't have room for a whole new Gateway, not as
guidance to always do this.

## Applying

1. Confirm free quota (see Prerequisites).
2. Add the second listener to the existing Gateway:

   ```bash
   export GATEWAY_NAME=inference-gateway
   export NAMESPACE=wetlab-ai
   export LISTENER_SECTION_NAME=openshift-istio-test
   export GATEWAY_LISTENER_PORT=8081
   envsubst < gateway-listener-patch.yaml > /tmp/listener-patch.json
   oc patch gateway "${GATEWAY_NAME}" -n "${NAMESPACE}" --type=json \
     --patch-file=/tmp/listener-patch.json
   ```

3. Render and apply the environment (values below match what was actually
   used for the live validation run - see
   `GATEWAY-ARCHITECTURE-KNOWN-RISKS.md` for the date):

   ```bash
   export POOL_NAME=openshift-istio-decode-pool
   export EPP_NAME=openshift-istio-decode-epp
   export MULTIMODAL_POOL_NAME=openshift-istio-multimodal-pool
   export MULTIMODAL_EPP_NAME=openshift-istio-multimodal-epp
   export COORDINATOR_IMAGE=quay.io/rh_et_wd/llm-d-coordinator:wetlab-demo
   export EPP_IMAGE=quay.io/rh_et_wd/llm-d-router-endpoint-picker:latest
   export VLLM_IMAGE=ghcr.io/llm-d/llm-d-inference-sim:v0.10.2
   export VLLM_SIM_MODE=echo
   export METRICS_ENDPOINT_AUTH=none
   export GATEWAY_SERVICE_NAME=inference-gateway-data-science-gateway-class

   kubectl kustomize . | envsubst | oc apply -n "${NAMESPACE}" -f -
   ```

4. Verify in isolation from the live demo, via port-forward to the new
   listener's port (not the public Route, which still only exposes port 80
   / the live demo's listener):

   ```bash
   oc port-forward -n "${NAMESPACE}" \
     svc/inference-gateway-data-science-gateway-class 18081:8081 &
   curl -s -X POST http://127.0.0.1:18081/v1/chat/completions \
     -H 'Content-Type: application/json' \
     -d '{"model":"any","messages":[{"role":"user","content":"hi"}],"max_tokens":5}'
   curl -s -o /tmp/oi-speech.out -w '%{http_code}\n' \
     http://127.0.0.1:18081/v1/audio/speech \
     -H 'Content-Type: application/json' \
     -d '{"model":"tts-1","input":"hello"}'
   ```

5. Tear down (this environment is additive and self-contained, safe to
   remove without touching the live demo):

   ```bash
   kubectl kustomize . | envsubst | oc delete -n "${NAMESPACE}" -f - --ignore-not-found
   oc patch gateway "${GATEWAY_NAME}" -n "${NAMESPACE}" --type=json \
     --patch='[{"op":"remove","path":"/spec/listeners/1"}]'
   ```

## The x-request-id correlation contract

Every leg of the double-hop preserves (or, if absent, the first leg's proxy
generates) an `x-request-id` header, and it is deliberately never stripped
by the Coordinator's header-forwarding logic
(`pkg/coordinator/pipeline/context.go`, `internalForwardingHeaders` /
`shouldSkipForwardedHeader` - only Envoy/Istio hop metadata and hop-by-hop
headers are dropped, `x-request-id` is explicitly preserved and tested by
`TestForwardedHeaders_ExcludesEnvoyHopMetadata`). This means `grep
x-request-id` against Gateway, Coordinator, and EPP logs for the same value
reliably reconstructs one client request's full four-leg path across all
four log streams - this is the supported way to debug or demo a specific
request's routing, not an incidental debugging convenience.
