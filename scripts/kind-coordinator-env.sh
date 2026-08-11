#!/bin/bash

# Deploys the coordinator alongside the EPP and a vLLM simulator on a kind
# cluster with Istio. External clients reach the coordinator through the Istio
# gateway NodePort (30080); the coordinator routes decode requests back through
# the same gateway to the decode InferencePool via EPP.

set -eo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# ------------------------------------------------------------------------------
# Variables
# ------------------------------------------------------------------------------

: "${CLUSTER_NAME:=llm-d-coordinator-dev}"
: "${GATEWAY_HOST_PORT:=30080}"
: "${IMAGE_REGISTRY:=ghcr.io/llm-d}"

export VLLM_SIMULATOR_TAG="${VLLM_SIMULATOR_TAG:-v0.10.2}"
export VLLM_IMAGE="${VLLM_IMAGE:-${IMAGE_REGISTRY}/llm-d-inference-sim:${VLLM_SIMULATOR_TAG}}"

export EPP_TAG="${EPP_TAG:-dev}"
EPP_IMAGE="${EPP_IMAGE:-${IMAGE_REGISTRY}/llm-d-router-endpoint-picker:${EPP_TAG}}"
export EPP_IMAGE

export COORDINATOR_TAG="${COORDINATOR_TAG:-dev}"
COORDINATOR_IMAGE="${COORDINATOR_IMAGE:-${IMAGE_REGISTRY}/llm-d-coordinator:${COORDINATOR_TAG}}"
export COORDINATOR_IMAGE

export MODEL_NAME="${MODEL_NAME:-TinyLlama/TinyLlama-1.1B-Chat-v1.0}"
export MODEL_ID="${MODEL_NAME##*/}"
# Safe model name for Kubernetes resources (lowercase, hyphenated)
export MODEL_NAME_SAFE
MODEL_NAME_SAFE=$(echo "${MODEL_ID}" | tr '[:upper:]' '[:lower:]' | tr ' /_.' '-')

export EPP_NAME="${EPP_NAME:-${MODEL_NAME_SAFE}-endpoint-picker}"
export POOL_NAME="${POOL_NAME:-${MODEL_NAME_SAFE}-inference-pool}"
export MULTIMODAL_EPP_NAME="${MULTIMODAL_EPP_NAME:-multimodal-endpoint-picker}"
export MULTIMODAL_POOL_NAME="${MULTIMODAL_POOL_NAME:-multimodal-inference-pool}"
# Name of the Gateway API Gateway object the HTTPRoutes attach to
# (deploy/coordinator/environments/dev/kind-istio/gateways.yaml). Kept as a
# variable, not a hardcoded literal, so the same httproutes.yaml can be
# re-applied against a differently-named Gateway (e.g. llm-d-inference-gateway
# on OpenShift/agentgateway) via scripts/apply-gateway-config.sh.
export GATEWAY_NAME="${GATEWAY_NAME:-inference-gateway}"
export NAMESPACE="${NAMESPACE:-default}"
export METRICS_ENDPOINT_AUTH="${METRICS_ENDPOINT_AUTH:-false}"

export VLLM_REPLICA_COUNT_D="${VLLM_REPLICA_COUNT_D:-1}"
export VLLM_DATA_PARALLEL_SIZE="${VLLM_DATA_PARALLEL_SIZE:-1}"
export VLLM_SIM_MODE="${VLLM_SIM_MODE:-echo}"
export KV_CACHE_ENABLED="${KV_CACHE_ENABLED:-false}"

# Role label for the decode pod; the coordinator's InferencePool selector
# matches llm-d.ai/role: decode, so this must remain "decode".
export DECODE_ROLE="${DECODE_ROLE:-decode}"

# Model architecture label (llm-d.ai/model-arch). Empty = standard decode pod.
export MODEL_ARCH="${MODEL_ARCH:-}"

# Extra vLLM args (empty by default). Use --flag=value format.
export VLLM_EXTRA_ARGS_D="${VLLM_EXTRA_ARGS_D:-}"

# Placeholders required by the base vllm-decode manifest. The coordinator's
# kustomize patches remove the routing-sidecar initContainer and replace the
# vllm container args list before deployment, so these values never reach any
# running container. They are exported only to satisfy envsubst.
export SIDECAR_IMAGE=""
export VLLM_RENDER_URL=""

export EPP_CONFIG="${EPP_CONFIG:-deploy/config/sim-epp-config.yaml}"

# ------------------------------------------------------------------------------
# Requirement checks
# ------------------------------------------------------------------------------

if [ -z "${CONTAINER_RUNTIME:-}" ]; then
  if command -v docker &>/dev/null; then
    CONTAINER_RUNTIME="docker"
  elif command -v podman &>/dev/null; then
    CONTAINER_RUNTIME="podman"
  else
    echo "Neither docker nor podman could be found in PATH" >&2
    exit 1
  fi
fi

set -u

for cmd in kind kubectl "${CONTAINER_RUNTIME}"; do
  if ! command -v "${cmd}" &>/dev/null; then
    echo "Error: ${cmd} is not installed or not in PATH." >&2
    exit 1
  fi
done

# ------------------------------------------------------------------------------
# Cluster
# ------------------------------------------------------------------------------

if kind get clusters 2>/dev/null | grep -q "^${CLUSTER_NAME}$"; then
  echo "Cluster '${CLUSTER_NAME}' already exists, re-using"
else
  kind create cluster --name "${CLUSTER_NAME}" --config - <<EOF
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
  # Pin to Kubernetes 1.31+ for Gateway API v1.5.1 compatibility
  # (requires isIP() CEL function and ValidatingAdmissionPolicy)
  image: kindest/node:v1.31.12
  extraPortMappings:
  - containerPort: 30080
    hostPort: ${GATEWAY_HOST_PORT}
    protocol: TCP
EOF
fi

KUBE_CONTEXT="kind-${CLUSTER_NAME}"
kubectl config set-context "${KUBE_CONTEXT}" --namespace=default

set -x

# Hotfix for https://github.com/kubernetes-sigs/kind/issues/3880
CONTAINER_NAME="${CLUSTER_NAME}-control-plane"
${CONTAINER_RUNTIME} exec "${CONTAINER_NAME}" /bin/bash -c "sysctl net.ipv4.conf.all.arp_ignore=0"

kubectl --context "${KUBE_CONTEXT}" -n kube-system wait --for=condition=Ready --all pods --timeout=300s

echo "Waiting for local-path-storage pods to be created..."
deadline=$(( $(date +%s) + 120 ))
until kubectl --context "${KUBE_CONTEXT}" -n local-path-storage get pods -o name 2>/dev/null | grep -q pod/; do
  if (( $(date +%s) >= deadline )); then
    echo "ERROR: local-path-storage pods did not appear within 120s" >&2
    kubectl --context "${KUBE_CONTEXT}" get namespaces >&2 || true
    kubectl --context "${KUBE_CONTEXT}" -n local-path-storage get pods >&2 || true
    exit 1
  fi
  sleep 2
done
kubectl --context "${KUBE_CONTEXT}" -n local-path-storage wait --for=condition=Ready --all pods --timeout=300s

# ------------------------------------------------------------------------------
# Load container images
# ------------------------------------------------------------------------------

LINUX_ARCH="$(uname -m)"
case "${LINUX_ARCH}" in
  x86_64)  LINUX_ARCH="amd64" ;;
  aarch64|arm64) LINUX_ARCH="arm64" ;;
esac

PLATFORM_ARGS=()
SAVE_ARGS=()
if [ "${CONTAINER_RUNTIME}" == "docker" ]; then
  PLATFORM_ARGS=("--platform" "linux/${LINUX_ARCH}")
elif [ "${CONTAINER_RUNTIME}" == "podman" ]; then
  SAVE_ARGS=("--format=docker-archive")
fi

pull_image() {
  local image="$1"
  if ! "${CONTAINER_RUNTIME}" image inspect "${image}" >/dev/null 2>&1; then
    echo "Image ${image} not found locally, pulling..."
    "${CONTAINER_RUNTIME}" pull ${PLATFORM_ARGS[@]+"${PLATFORM_ARGS[@]}"} "${image}"
  fi
}

load_image() {
  local image="$1"
  echo "Loading ${image} into kind cluster..."
  if [ "${CONTAINER_RUNTIME}" == "docker" ]; then
    docker save "${image}" | \
      docker exec --privileged -i "${CLUSTER_NAME}-control-plane" \
      ctr --namespace=k8s.io images import --digests --snapshotter=overlayfs -
  else
    "${CONTAINER_RUNTIME}" save ${SAVE_ARGS[@]+"${SAVE_ARGS[@]}"} "${image}" | kind --name "${CLUSTER_NAME}" load image-archive /dev/stdin
  fi
}

for IMAGE in "${VLLM_IMAGE}" "${EPP_IMAGE}" "${COORDINATOR_IMAGE}"; do
  pull_image "${IMAGE}"
  load_image "${IMAGE}"
done

# ------------------------------------------------------------------------------
# CRD deployment
# ------------------------------------------------------------------------------

# apply_crds retries up to 3 times; etcd occasionally times out on large CRD
# sets (e.g. Istio) and --server-side --force-conflicts is idempotent.
apply_crds() {
  local kustomize_extra_flags="$1"
  local kustomize_dir="$2"
  local attempt max_attempts=3
  for attempt in $(seq 1 "${max_attempts}"); do
    if kubectl kustomize ${kustomize_extra_flags} "${kustomize_dir}" \
           | kubectl --context "${KUBE_CONTEXT}" apply --server-side --force-conflicts -f -; then
      return 0
    fi
    if [ "${attempt}" -lt "${max_attempts}" ]; then
      echo "CRD apply failed (attempt ${attempt}/${max_attempts}), retrying in 5s..." >&2
      sleep 5
    fi
  done
  echo "Error: CRD apply failed after ${max_attempts} attempts: ${kustomize_dir}" >&2
  return 1
}

apply_crds ""               deploy/components/crds-gateway-api
apply_crds ""               deploy/components/crds-gie
apply_crds ""               config/crd
apply_crds "--enable-helm"  deploy/components/crds-istio

# ------------------------------------------------------------------------------
# ConfigMaps
# ------------------------------------------------------------------------------

TEMP_FILE=$(mktemp)
trap 'rm -f "${TEMP_FILE}"' EXIT

# Decode EPP configuration. Uses the same config as the non-disaggregated EPP
# in the standard dev environment.
kubectl --context "${KUBE_CONTEXT}" delete configmap epp-config --ignore-not-found
envsubst '$MODEL_NAME' < "${EPP_CONFIG}" > "${TEMP_FILE}"
kubectl --context "${KUBE_CONTEXT}" create configmap epp-config --from-file=epp-config.yaml="${TEMP_FILE}"

# Multimodal EPP configuration. Uses the modality-filter plugin to route
# TTS/STT/image requests to pods matching llm-d.ai/model-arch labels.
kubectl --context "${KUBE_CONTEXT}" delete configmap multimodal-epp-config --ignore-not-found
kubectl --context "${KUBE_CONTEXT}" create configmap multimodal-epp-config \
  --from-file=epp-config.yaml=deploy/config/sim-multimodal-epp-config.yaml

# Coordinator configuration. The gateway address is inference-gateway-istio:80,
# the ClusterIP Service Istio creates automatically for the Gateway resource.
kubectl --context "${KUBE_CONTEXT}" delete configmap llm-d-coordinator-config --ignore-not-found
cat > "${TEMP_FILE}" <<'COORDINATOR_CONFIG'
log_level: 4
server:
  listen_addr: ":8080"
  read_timeout: 30s
  write_timeout: 300s
  shutdown_timeout: 25s
gateway:
  address: "http://inference-gateway-istio:80"
  timeout: 300s
pipeline:
  use_openai_format: true
  steps:
  - type: decode
COORDINATOR_CONFIG
kubectl --context "${KUBE_CONTEXT}" create configmap llm-d-coordinator-config --from-file=coordinator.yaml="${TEMP_FILE}"

# ------------------------------------------------------------------------------
# Deployment
# ------------------------------------------------------------------------------

KUSTOMIZE_DIR="deploy/coordinator/environments/dev/kind-istio"

kubectl kustomize --enable-helm "${KUSTOMIZE_DIR}" \
  | envsubst '${POOL_NAME} ${MODEL_NAME} ${MODEL_NAME_SAFE} ${EPP_NAME} ${EPP_IMAGE} ${VLLM_IMAGE} \
  ${COORDINATOR_IMAGE} ${SIDECAR_IMAGE} ${VLLM_RENDER_URL} ${NAMESPACE} ${METRICS_ENDPOINT_AUTH} \
  ${DECODE_ROLE} ${MODEL_ARCH} ${KV_CACHE_ENABLED} ${VLLM_SIM_MODE} \
  ${VLLM_REPLICA_COUNT_D} ${VLLM_DATA_PARALLEL_SIZE} ${VLLM_EXTRA_ARGS_D} \
  ${MULTIMODAL_EPP_NAME} ${MULTIMODAL_POOL_NAME} ${GATEWAY_NAME}' \
  | awk '
    /^[[:space:]]*-[[:space:]]+".*"[[:space:]]*$/ {
      match($0, /^[[:space:]]*/); indent = substr($0, 1, RLENGTH)
      content = $0
      sub(/^[[:space:]]*-[[:space:]]+"/, "", content)
      sub(/"[[:space:]]*$/, "", content)
      if (content == "") { print; next }
      if (substr(content, 1, 2) == "--") {
        n = split(content, flags, " --")
        for (i = 1; i <= n; i++) {
          flag = flags[i]
          if (i > 1) flag = "--" flag
          if (flag != "") print indent "- \"" flag "\""
        }
        next
      }
    }
    { print }
  ' \
  | kubectl --context "${KUBE_CONTEXT}" apply -f -

# ------------------------------------------------------------------------------
# Wait for readiness
# ------------------------------------------------------------------------------

kubectl --context "${KUBE_CONTEXT}" -n llm-d-istio-system wait --for=condition=available --timeout=600s deployment --all
kubectl --context "${KUBE_CONTEXT}" -n default wait --for=condition=available --timeout=600s deployment --all
kubectl --context "${KUBE_CONTEXT}" wait gateway/inference-gateway --for=condition=Programmed --timeout=600s

# ------------------------------------------------------------------------------
# Summary
# ------------------------------------------------------------------------------

cat <<EOF
-----------------------------------------
Coordinator dev environment deployed!

* Kind Cluster Name: ${CLUSTER_NAME}
* Kubectl Context: ${KUBE_CONTEXT}

Architecture:
* External clients -> Istio gateway (localhost:${GATEWAY_HOST_PORT}) -> coordinator
* Coordinator -> Istio gateway -> decode InferencePool -> EPP -> vLLM simulator

Watch coordinator logs:
  \$ kubectl --context ${KUBE_CONTEXT} logs -f deployments/llm-d-coordinator

Watch EPP logs:
  \$ kubectl --context ${KUBE_CONTEXT} logs -f deployments/${EPP_NAME}-decode

Test chat completions (coordinator pipeline):
  \$ curl -s http://localhost:${GATEWAY_HOST_PORT}/v1/chat/completions \\
      -H 'Content-Type: application/json' \\
      -d '{"model":"${MODEL_NAME}","messages":[{"role":"user","content":"hello"}],"max_tokens":10}' | jq

Test TTS (passthrough to gateway):
  \$ curl -s http://localhost:${GATEWAY_HOST_PORT}/v1/audio/speech \\
      -H 'Content-Type: application/json' \\
      -d '{"model":"tts-1","input":"hello"}' | jq
-----------------------------------------
EOF
