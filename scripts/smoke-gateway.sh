#!/usr/bin/env bash
# Smoke-test the coordinator + Gateway RFC path on kind (Istio) or OpenShift
# (agentgateway).
#
# Proves routing (not model quality). TTS/STT sims may return 404; we assert
# the request reached the right pod via logs, and that chat returns 200 JSON.
#
# Prereq:
#   kind:      make -f Makefile.coord.mk env-dev-kind-coordinator
#              ./scripts/apply-gateway-config.sh   # optional refresh
#   openshift: coordinator stack + Gateway already deployed;
#              PLATFORM=openshift ./scripts/apply-gateway-config.sh
#
# Usage:
#   ./scripts/smoke-gateway.sh                                  # kind
#   PLATFORM=openshift KUBE_CONTEXT=... GATEWAY_URL=https://... \
#     POOL_NAME=... GATEWAY_NAME=llm-d-inference-gateway \
#     ./scripts/smoke-gateway.sh                                # openshift

set -euo pipefail

PLATFORM="${PLATFORM:-kind}" # kind | openshift

case "${PLATFORM}" in
  kind)
    CTX="${KUBE_CONTEXT:-kind-llm-d-coordinator-dev}"
    BASE="${GATEWAY_URL:-http://localhost:30080}"
    GATEWAY_NAME="${GATEWAY_NAME:-inference-gateway}"
    ;;
  openshift)
    CTX="${KUBE_CONTEXT:-$(kubectl config current-context)}"
    : "${GATEWAY_URL:?GATEWAY_URL must be set for PLATFORM=openshift (external Gateway URL or port-forward address)}"
    BASE="${GATEWAY_URL}"
    GATEWAY_NAME="${GATEWAY_NAME:-llm-d-inference-gateway}"
    ;;
  *)
    echo "ERROR: PLATFORM must be 'kind' or 'openshift', got '${PLATFORM}'" >&2
    exit 1
    ;;
esac

NAMESPACE="${NAMESPACE:-default}"
MODEL="${MODEL_NAME:-TinyLlama/TinyLlama-1.1B-Chat-v1.0}"
MODEL_ID="${MODEL##*/}"
MODEL_NAME_SAFE="$(echo "${MODEL_ID}" | tr '[:upper:]' '[:lower:]' | tr ' /_.' '-')"
POOL_NAME="${POOL_NAME:-${MODEL_NAME_SAFE}-inference-pool}"

PASS=0
FAIL=0

green() { printf '\033[32m✓ %s\033[0m\n' "$*"; PASS=$((PASS + 1)); }
red()   { printf '\033[31m✗ %s\033[0m\n' "$*"; FAIL=$((FAIL + 1)); }
info()  { printf '  %s\n' "$*"; }

need() { command -v "$1" >/dev/null || { echo "missing $1"; exit 1; }; }
need curl
need kubectl
need jq

kubectl --context "${CTX}" cluster-info >/dev/null 2>&1 \
  || { echo "Cannot reach ${CTX}"; exit 1; }

echo "==> Smoke test against ${BASE} (platform=${PLATFORM}, context ${CTX}, namespace ${NAMESPACE})"
echo

# --- 1. Chat (full Coordinator → decode pool path) ---------------------------
echo "1) Chat completions (expect HTTP 200 + assistant content)"
CHAT_CODE="$(curl -sS -o /tmp/smoke-chat.json -w '%{http_code}' \
  "${BASE}/v1/chat/completions" \
  -H 'Content-Type: application/json' \
  -d "{\"model\":\"${MODEL}\",\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}],\"max_tokens\":5}")"
if [[ "${CHAT_CODE}" == "200" ]] && jq -e '.choices[0].message.content' /tmp/smoke-chat.json >/dev/null 2>&1; then
  green "chat → ${CHAT_CODE} ($(jq -r '.choices[0].message.content' /tmp/smoke-chat.json | head -c 40))"
else
  red "chat → ${CHAT_CODE} (body: $(head -c 120 /tmp/smoke-chat.json))"
fi

# --- 2. TTS routing (Coordinator passthrough → multimodal EPP → TTS pod) -----
echo "2) TTS /v1/audio/speech (expect POST on vllm-tts-sim)"
TTS_CODE="$(curl -sS -o /tmp/smoke-tts.out -w '%{http_code}' \
  "${BASE}/v1/audio/speech" \
  -H 'Content-Type: application/json' \
  -d '{"model":"tts-1","input":"hello from smoke test"}')"
sleep 1
if kubectl --context "${CTX}" -n "${NAMESPACE}" logs deploy/vllm-tts-sim --since=30s 2>/dev/null \
    | grep -a 'requestURI="/v1/audio/speech"' >/dev/null; then
  green "TTS routed to vllm-tts-sim (upstream HTTP ${TTS_CODE}; sim may 404)"
else
  red "TTS did not hit vllm-tts-sim (client HTTP ${TTS_CODE})"
fi

# --- 3. STT routing ----------------------------------------------------------
echo "3) STT /v1/audio/transcriptions (expect EPP pick vllm-stt-sim)"
STT_CODE="$(curl -sS -o /tmp/smoke-stt.out -w '%{http_code}' \
  "${BASE}/v1/audio/transcriptions" \
  -H 'Content-Type: application/json' \
  -d '{"model":"whisper-1","file":"x"}')"
sleep 1
# Sims may not log every path; multimodal EPP logs the chosen endpoint.
if kubectl --context "${CTX}" -n "${NAMESPACE}" logs deploy/multimodal-endpoint-picker --since=30s 2>/dev/null \
    | grep -aE 'vllm-stt-sim|encoder-decoder-stt' >/dev/null; then
  green "STT EPP selected vllm-stt-sim (upstream HTTP ${STT_CODE}; sim may 404)"
elif kubectl --context "${CTX}" -n "${NAMESPACE}" logs deploy/vllm-stt-sim --since=30s 2>/dev/null \
    | grep -aE 'transcriptions|requestURI=.*audio' >/dev/null; then
  green "STT routed to vllm-stt-sim (upstream HTTP ${STT_CODE}; sim may 404)"
else
  red "STT did not select vllm-stt-sim (client HTTP ${STT_CODE})"
fi

# --- 4. Multimodal header path (second hop) ----------------------------------
echo "4) Direct EPP-Profile: multimodal (Gateway → multimodal pool, skip Coordinator)"
DIRECT_CODE="$(curl -sS -o /tmp/smoke-direct.out -w '%{http_code}' \
  "${BASE}/v1/audio/speech" \
  -H 'Content-Type: application/json' \
  -H 'EPP-Profile: multimodal' \
  -d '{"model":"tts-1","input":"direct hop"}')"
sleep 1
if kubectl --context "${CTX}" -n "${NAMESPACE}" logs deploy/vllm-tts-sim --since=15s 2>/dev/null \
    | grep -a 'requestURI="/v1/audio/speech"' >/dev/null; then
  green "header multimodal hop reached TTS pod (HTTP ${DIRECT_CODE})"
else
  red "header multimodal hop missed TTS pod (HTTP ${DIRECT_CODE})"
fi

# --- 5. No 431 loop ----------------------------------------------------------
echo "5) No HTTP 431 (request-loop / header blow-up)"
if [[ "${CHAT_CODE}" == "431" || "${TTS_CODE}" == "431" || "${STT_CODE}" == "431" ]]; then
  red "saw HTTP 431 — check for path routes stealing EPP-Profile: multimodal"
else
  green "no 431 on chat/TTS/STT"
fi

# --- 6. Gateway API objects Ready ---------------------------------------------
echo "6) Gateway API objects"
if kubectl --context "${CTX}" -n "${NAMESPACE}" get gateway "${GATEWAY_NAME}" \
    -o jsonpath='{.status.conditions[?(@.type=="Programmed")].status}' 2>/dev/null | grep -q True; then
  green "Gateway/${GATEWAY_NAME} PROGRAMMED=True"
else
  red "Gateway/${GATEWAY_NAME} not PROGRAMMED"
fi
for r in multimodal-route "${POOL_NAME}-decode-route" "${POOL_NAME}-coordinator-route"; do
  if kubectl --context "${CTX}" -n "${NAMESPACE}" get httproute "${r}" >/dev/null 2>&1; then
    green "HTTPRoute/${r} exists"
  else
    red "HTTPRoute/${r} missing"
  fi
done

# --- 7. Mixed traffic: text still works after multimodal calls (RFC MVP gate) -
echo "7) Text unaffected after multimodal traffic (RFC MVP gate)"
MIXED_CODE="$(curl -sS -o /tmp/smoke-mixed.json -w '%{http_code}' \
  "${BASE}/v1/chat/completions" \
  -H 'Content-Type: application/json' \
  -d "{\"model\":\"${MODEL}\",\"messages\":[{\"role\":\"user\",\"content\":\"still there?\"}],\"max_tokens\":5}")"
if [[ "${MIXED_CODE}" == "200" ]] && jq -e '.choices[0].message.content' /tmp/smoke-mixed.json >/dev/null 2>&1; then
  green "chat still 200 after multimodal traffic"
else
  red "chat degraded after multimodal traffic (HTTP ${MIXED_CODE})"
fi

echo
echo "Results: ${PASS} passed, ${FAIL} failed"
[[ "${FAIL}" -eq 0 ]]
