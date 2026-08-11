#!/usr/bin/env bash
# Apply / refresh Gateway API config for the coordinator environment, on
# either kind (Istio) or OpenShift (agentgateway).
#
# This does NOT install a Gateway provider. It assumes:
#   - kind:      make -f Makefile.coord.mk env-dev-kind-coordinator
#                (creates Gateway/inference-gateway via Istio)
#   - openshift: the coordinator stack (Coordinator + text/multimodal
#                InferencePool + EPP + workers) is already deployed, and a
#                Gateway named ${GATEWAY_NAME} already exists, e.g. via:
#                  kubectl apply -k \
#                    <llm-d checkout>/guides/recipes/gateway/agentgateway-openshift \
#                    -n ${NAMESPACE}
#
# This script only wires HTTPRoutes (+ DestinationRules on kind/Istio) and
# confirms the Coordinator → gateway address matches the RFC path:
#
#   Client → Gateway → Coordinator → Gateway (EPP-Profile) → InferencePool → pods
#
# Usage:
#   ./scripts/apply-gateway-config.sh                       # kind (default)
#   PLATFORM=openshift KUBE_CONTEXT=... NAMESPACE=... \
#     GATEWAY_ADDRESS=http://<in-cluster-gateway-svc>:80 \
#     ./scripts/apply-gateway-config.sh                      # openshift

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${ROOT_DIR}"

PLATFORM="${PLATFORM:-kind}" # kind | openshift

case "${PLATFORM}" in
  kind)
    CLUSTER_NAME="${CLUSTER_NAME:-llm-d-coordinator-dev}"
    KUBE_CONTEXT="${KUBE_CONTEXT:-kind-${CLUSTER_NAME}}"
    GATEWAY_NAME="${GATEWAY_NAME:-inference-gateway}"
    GATEWAY_HOST_PORT="${GATEWAY_HOST_PORT:-30080}"
    ;;
  openshift)
    KUBE_CONTEXT="${KUBE_CONTEXT:-$(kubectl config current-context)}"
    GATEWAY_NAME="${GATEWAY_NAME:-llm-d-inference-gateway}"
    ;;
  *)
    echo "ERROR: PLATFORM must be 'kind' or 'openshift', got '${PLATFORM}'" >&2
    exit 1
    ;;
esac

NAMESPACE="${NAMESPACE:-default}"

MODEL_NAME="${MODEL_NAME:-TinyLlama/TinyLlama-1.1B-Chat-v1.0}"
MODEL_ID="${MODEL_NAME##*/}"
MODEL_NAME_SAFE="$(echo "${MODEL_ID}" | tr '[:upper:]' '[:lower:]' | tr ' /_.' '-')"
POOL_NAME="${POOL_NAME:-${MODEL_NAME_SAFE}-inference-pool}"
EPP_NAME="${EPP_NAME:-${MODEL_NAME_SAFE}-endpoint-picker}"
MULTIMODAL_POOL_NAME="${MULTIMODAL_POOL_NAME:-multimodal-inference-pool}"
MULTIMODAL_EPP_NAME="${MULTIMODAL_EPP_NAME:-multimodal-endpoint-picker}"

log()  { printf '==> %s\n' "$*"; }
info() { printf '    %s\n' "$*"; }
die()  { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

command -v kubectl >/dev/null || die "kubectl required"
command -v envsubst >/dev/null || die "envsubst required (gettext package)"
kubectl --context "${KUBE_CONTEXT}" cluster-info >/dev/null 2>&1 \
  || die "Cannot reach context ${KUBE_CONTEXT}."

log "Using context ${KUBE_CONTEXT} (platform=${PLATFORM}, namespace=${NAMESPACE})"

kubectl --context "${KUBE_CONTEXT}" get gateway "${GATEWAY_NAME}" -n "${NAMESPACE}" >/dev/null \
  || die "Gateway/${GATEWAY_NAME} missing in namespace ${NAMESPACE}. \
For kind: run 'make -f Makefile.coord.mk env-dev-kind-coordinator' first. \
For openshift: install the Gateway via the llm-d agentgateway-openshift recipe first."

# --- Gateway in-cluster address (for the Coordinator's second hop) -----------
if [[ -z "${GATEWAY_ADDRESS:-}" ]]; then
  case "${PLATFORM}" in
    kind)
      # Known Istio-generated ClusterIP Service DNS name for this Gateway.
      GATEWAY_ADDRESS="http://${GATEWAY_NAME}-istio:80"
      ;;
    openshift)
      # Gateway API status.addresses is populated by any spec-compliant
      # controller (agentgateway included), so this works without hardcoding
      # a provider-specific Service naming convention.
      DISCOVERED="$(kubectl --context "${KUBE_CONTEXT}" get gateway "${GATEWAY_NAME}" -n "${NAMESPACE}" \
        -o jsonpath='{.status.addresses[0].value}' 2>/dev/null || true)"
      if [[ -z "${DISCOVERED}" ]]; then
        die "Could not auto-discover Gateway/${GATEWAY_NAME} address (status.addresses empty). \
Set GATEWAY_ADDRESS explicitly, e.g. GATEWAY_ADDRESS=http://<gateway-service>.${NAMESPACE}.svc.cluster.local:80"
      fi
      GATEWAY_ADDRESS="http://${DISCOVERED}:80"
      info "Discovered Gateway address: ${GATEWAY_ADDRESS}"
      ;;
  esac
fi

# --- HTTPRoutes (+ DestinationRules on kind/Istio only) ----------------------
log "Applying Gateway HTTPRoutes"
export POOL_NAME EPP_NAME MULTIMODAL_POOL_NAME MULTIMODAL_EPP_NAME GATEWAY_NAME
envsubst '${POOL_NAME} ${EPP_NAME} ${MULTIMODAL_POOL_NAME} ${MULTIMODAL_EPP_NAME} ${GATEWAY_NAME}' \
  < deploy/coordinator/environments/dev/kind-istio/httproutes.yaml \
  | kubectl --context "${KUBE_CONTEXT}" apply -n "${NAMESPACE}" -f -

if [[ "${PLATFORM}" == "kind" ]]; then
  log "Applying Istio DestinationRules (kind-only; no agentgateway equivalent)"
  envsubst '${EPP_NAME} ${MULTIMODAL_EPP_NAME}' \
    < deploy/coordinator/environments/dev/kind-istio/destination-rules.yaml \
    | kubectl --context "${KUBE_CONTEXT}" apply -n "${NAMESPACE}" -f -
else
  log "Skipping DestinationRules (Istio-only resource, not applicable to agentgateway)"
fi

# --- Coordinator gateway address ---------------------------------------------
log "Ensuring Coordinator gateway.address → ${GATEWAY_ADDRESS}"
TEMP="$(mktemp)"
trap 'rm -f "${TEMP}"' EXIT
cat > "${TEMP}" <<EOF
log_level: 4
server:
  listen_addr: ":8080"
  read_timeout: 30s
  write_timeout: 300s
  shutdown_timeout: 25s
gateway:
  address: "${GATEWAY_ADDRESS}"
  timeout: 300s
pipeline:
  use_openai_format: true
  steps:
  - type: decode
EOF
kubectl --context "${KUBE_CONTEXT}" -n "${NAMESPACE}" delete configmap llm-d-coordinator-config --ignore-not-found
kubectl --context "${KUBE_CONTEXT}" -n "${NAMESPACE}" create configmap llm-d-coordinator-config \
  --from-file=coordinator.yaml="${TEMP}"
kubectl --context "${KUBE_CONTEXT}" -n "${NAMESPACE}" rollout restart deploy/llm-d-coordinator
kubectl --context "${KUBE_CONTEXT}" -n "${NAMESPACE}" rollout status deploy/llm-d-coordinator --timeout=180s

# --- Status -------------------------------------------------------------------
log "Gateway objects"
kubectl --context "${KUBE_CONTEXT}" -n "${NAMESPACE}" get gateway,httproute,inferencepool

cat <<EOF

-----------------------------------------
Gateway config applied (platform=${PLATFORM})

Flow (RFC):
$([[ "${PLATFORM}" == "kind" ]] && echo "  Client → http://localhost:${GATEWAY_HOST_PORT}" || echo "  Client → <external Gateway address for ${GATEWAY_NAME}>")
        → Gateway/${GATEWAY_NAME}
        → Coordinator
        → Gateway again (EPP-Profile: decode|multimodal)
        → InferencePool + EPP → workers

Refresh smoke tests:
  PLATFORM=${PLATFORM} KUBE_CONTEXT=${KUBE_CONTEXT} GATEWAY_NAME=${GATEWAY_NAME} \\
    POOL_NAME=${POOL_NAME} ./scripts/smoke-gateway.sh

Do not add path-only HTTPRoutes for /v1/audio/* → Coordinator: they steal
EPP-Profile: multimodal re-entry hops and cause HTTP 431 loops.
-----------------------------------------
EOF
