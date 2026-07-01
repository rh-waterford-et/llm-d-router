# Multimodal Endpoint Routing Setup Guide

Complete setup for testing multimodal endpoint routing (TTS, STT, image generation) with EPP in a local kind cluster.

**PR:** [#5 - Test routing to multimodal endpoints](https://github.com/rh-waterford-et/llm-d-router/pull/5)

**PR:** [#6 - Update Kind Deployment](https://github.com/rh-waterford-et/llm-d-router/pull/6)

## Code Changes

### Files Modified
1. **`pkg/epp/framework/plugins/requesthandling/parsers/openai/openai.go`**
   - Added multimodal endpoint constants: `audioSpeechAPI`, `audioTranscriptionsAPI`, `imagesGenerationsAPI`
   - Updated path detection to recognize new endpoints
   - Bypassed strict validation for multimodal requests

2. **`pkg/common/multimodal.go`** (NEW)
   - Shared constants for multimodal paths

3. **`pkg/sidecar/proxy/proxy.go`**
   - Registered handlers for new multimodal endpoints

### Deployment Files (deploy/kind/)
- `kind-cluster-config.yaml` - Kind cluster configuration
- `inferencepool-crd.yaml` - InferencePool CRD
- `inference.networking.x-k8s.io_inferenceobjectives.yaml` - InferenceObjective CRD (required)
- `epp-deployment.yaml` - EPP with flags: `--secure-serving=false --health-checking=true`
- `epp-config-multimodal.yaml` - ConfigMap with modality-filter plugin
- `envoy-gateway.yaml` - Envoy v1.33.2 with ext-proc (FULL_DUPLEX_STREAMED mode)
- `inferencepool.yaml` - InferencePool resource
- `multimodal-vllm-sims.yaml` - 4 backend pods (TTS, STT, Image, LLM)
- `setup-kind-cluster.sh` - Automated deployment script

## Setup

### Prerequisites
```bash
# Install Kind:
 
https://kind.sigs.k8s.io/docs/user/quick-start/installation
```

```bash
# Build images
make podman-build-epp
make podman-build-sidecar
```

### Quick Start (Automated)
```bash
cd deploy/kind
./setup-kind-cluster.sh
```

### Manual Setup
```bash
cd deploy/kind

# Create cluster
kind create cluster --config kind-cluster-config.yaml

# Load images
kind load docker-image ghcr.io/llm-d/llm-d-inference-scheduler:dev --name llm-d-inference-scheduler-dev
kind load docker-image ghcr.io/llm-d/llm-d-router-disagg-sidecar:dev --name llm-d-inference-scheduler-dev

# Deploy everything
kubectl apply -f inferencepool-crd.yaml
kubectl apply -f inference.networking.x-k8s.io_inferenceobjectives.yaml
kubectl apply -f epp-deployment.yaml
kubectl apply -f epp-config-multimodal.yaml
kubectl apply -f envoy-gateway.yaml
kubectl apply -f inferencepool.yaml
kubectl apply -f multimodal-vllm-sims.yaml

# Wait for pods
kubectl wait --for=condition=available deployment/food-review-endpoint-picker --timeout=120s
kubectl wait --for=condition=available deployment/inference-gateway --timeout=120s
kubectl wait --for=condition=ready pod -l llm-d.ai/inferenceServing=true --timeout=120s
```

## Testing

### Port Forward & Test
```bash
# Port forward gateway
kubectl port-forward svc/inference-gateway 8080:80 &

# Test TTS
curl -X POST http://localhost:8080/v1/audio/speech \
  -H 'Content-Type: application/json' \
  -d '{"model":"tts-1","messages":[{"role":"user","content":"test"}]}' \
  -w "\nHTTP: %{http_code}\n"

# Test STT
curl -X POST http://localhost:8080/v1/audio/transcriptions \
  -H 'Content-Type: application/json' \
  -d '{"model":"whisper-1","messages":[{"role":"user","content":"test"}]}' \
  -w "\nHTTP: %{http_code}\n"

# Test Image
curl -X POST http://localhost:8080/v1/images/generations \
  -H 'Content-Type: application/json' \
  -d '{"model":"dall-e-3","messages":[{"role":"user","content":"test"}]}' \
  -w "\nHTTP: %{http_code}\n"

# Test LLM
curl -X POST http://localhost:8080/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"llama-3","messages":[{"role":"user","content":"test"}]}' \
  -w "\nHTTP: %{http_code}\n"
```

**Expected:** HTTP 404 or other response (proves routing works: Envoy → EPP → Backend)

## Verify

```bash
# Check all pods running
kubectl get pods

# Verify EPP discovered all 4 backends
kubectl get pods -l llm-d.ai/inferenceServing=true --show-labels

# Check EPP is refreshing metrics for all pods
kubectl logs deployment/food-review-endpoint-picker | grep "Refreshed metrics" | tail -4

# See EPP processing requests
kubectl logs deployment/food-review-endpoint-picker --tail=100 | grep -E "Processing|checking|decoding"
```

**Pod IP Mapping** (from EPP logs):
```bash
kubectl logs deployment/food-review-endpoint-picker | grep "Current Pods and metrics gathered" -A5
```

Expected: 4 pods with labels `autoregressive-tts`, `encoder-decoder-stt`, `diffusion`, `autoregressive-llm`

## Cleanup
```bash
kind delete cluster --name llm-d-inference-scheduler-dev
```

## Architecture

```
Client → Envoy Gateway (ext-proc) → EPP (modality-filter) → Backend Pods
```

- EPP filters backends by `llm-d.ai/model-arch` label based on endpoint path
- `/v1/audio/speech` → `autoregressive-tts`
- `/v1/audio/transcriptions` → `encoder-decoder-stt`
- `/v1/images/generations` → `diffusion`
- `/v1/chat/completions` → `autoregressive-llm`
