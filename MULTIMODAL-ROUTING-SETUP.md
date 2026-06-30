# Multimodal Endpoint Routing Setup Guide

This document describes the complete setup for testing multimodal endpoint routing with EPP (Endpoint Picker) in a local kind cluster.

## Overview

This work added support for routing multimodal inference requests (TTS, STT, image generation) through EPP's modality filter based on endpoint paths and pod labels. Previously, EPP only recognized `/v1/chat/completions` and `/v1/completions` endpoints.

**PR:** [#5 - Test routing to multimodal endpoints](https://github.com/rh-waterford-et/llm-d-router/pull/5)

## Architecture

```
Client Request
  ↓ /v1/audio/speech, /v1/audio/transcriptions, /v1/images/generations
Envoy Gateway (inference-gateway-istio)
  ↓ gRPC ext-proc
EPP (Endpoint Picker)
  ↓ modality-filter plugin
  ↓ reads endpoint path + llm-d.ai/model-arch labels
Backend Pods (TTS/STT/Image/LLM)
  ↓ routing-sidecar + vllm-sim containers
Response
```

## Code Changes

### 1. EPP Request Parser

**File:** `pkg/epp/framework/plugins/requesthandling/parsers/openai/openai.go`

**Added multimodal endpoint constants:**
```go
const (
    chatCompletionsAPI        = "chat/completions"
    completionsAPI            = "completions"
    audioSpeechAPI            = "audio/speech"           // NEW
    audioTranscriptionsAPI    = "audio/transcriptions"  // NEW
    imagesGenerationsAPI      = "images/generations"    // NEW
    inferenceAPI              = "inference"             // NEW
)
```

**Updated path detection:**
```go
func determineAPITypeFromPath(path string) string {
    // Use suffix matching instead of exact path matching
    if strings.HasSuffix(path, chatCompletionsAPI) {
        return chatCompletionsAPI
    }
    if strings.HasSuffix(path, completionsAPI) {
        return completionsAPI
    }
    if strings.HasSuffix(path, audioSpeechAPI) {
        return audioSpeechAPI
    }
    if strings.HasSuffix(path, audioTranscriptionsAPI) {
        return audioTranscriptionsAPI
    }
    if strings.HasSuffix(path, imagesGenerationsAPI) {
        return imagesGenerationsAPI
    }
    if strings.HasSuffix(path, inferenceAPI) {
        return inferenceAPI
    }
    return unknownAPI
}
```

**Bypassed strict validation for multimodal:**
```go
func (p *Parser) extractRequestBody(ctx context.Context, req *extprocv3.ProcessingRequest) (map[string]any, error) {
    apiType := determineAPITypeFromPath(path)
    
    // Skip strict schema validation for multimodal endpoints
    // They have different request schemas than chat/completions
    if apiType == audioSpeechAPI || 
       apiType == audioTranscriptionsAPI || 
       apiType == imagesGenerationsAPI ||
       apiType == inferenceAPI {
        // Accept request without validation
        return bodyMap, nil
    }
    
    // Standard validation for chat/completions
    // ...
}
```

### 2. Common Package

**File:** `pkg/common/multimodal.go`

Added shared constants for consistent path handling:

```go
package common

const (
    AudioSpeechPath          = "/v1/audio/speech"
    AudioTranscriptionsPath  = "/v1/audio/transcriptions"
    ImagesGenerationsPath    = "/v1/images/generations"
    InferencePath            = "/v1/inference"
)
```

### 3. Routing Sidecar

**File:** `pkg/sidecar/proxy/proxy.go`

Updated to use common package constants:

```go
import "github.com/llm-d/llm-d-router/pkg/common"

func (p *Proxy) registerHandlers() {
    p.router.Post(common.AudioSpeechPath, p.handleInference)
    p.router.Post(common.AudioTranscriptionsPath, p.handleInference)
    p.router.Post(common.ImagesGenerationsPath, p.handleInference)
    p.router.Post(common.InferencePath, p.handleInference)
    // ... existing handlers
}
```

## Kind Cluster Setup

### Cluster Configuration

**File:** `kind-cluster-config.yaml` (in llm-d repo)

```yaml
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
name: llm-d-inference-scheduler-dev
nodes:
- role: control-plane
  extraPortMappings:
  - containerPort: 30080
    hostPort: 8080    # EPP/Gateway
  - containerPort: 30081
    hostPort: 8081    # TTS backend
  - containerPort: 30082
    hostPort: 8082    # STT backend
  - containerPort: 30083
    hostPort: 8083    # Image backend
  - containerPort: 30084
    hostPort: 8084    # LLM backend
```

### Create Cluster

```bash
kind create cluster --config kind-cluster-config.yaml
```

## Backend Pod Deployments

### Architecture

Each backend pod has **2 containers**:

1. **routing-sidecar** (port 8000)
   - Handles disaggregation protocol
   - Proxies to vllm container
   - Reports metrics to EPP via ZMQ

2. **vllm** (port 8200)
   - Mock inference backend (vllm-sim)
   - Returns simulated responses
   - Reports metrics on `/metrics` endpoint

### TTS Backend

**File:** `multimodal-vllm-sims.yaml`

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: tts-vllm-sim
  namespace: default
spec:
  template:
    metadata:
      labels:
        app: food-review-inference-pool
        llm-d.ai/model-arch: autoregressive-tts
        llm-d.ai/inferenceServing: "true"
    spec:
      containers:
      - name: routing-sidecar
        image: ghcr.io/llm-d/llm-d-router-disagg-sidecar:dev
        args:
        - --port=8000
        - --vllm-port=8200
        - --kv-connector=nixlv2
        - --secure-proxy=false
        - --data-parallel-size=1
        ports:
        - containerPort: 8000
          name: sidecar-http
        env:
        - name: POD_IP
          valueFrom:
            fieldRef:
              fieldPath: status.podIP
              
      - name: vllm
        image: ghcr.io/llm-d/llm-d-inference-sim:latest
        args:
        - --port=8200
        - --model=tts-1
        - --enable-kvcache=false
        - --kv-cache-size=1024
        - --block-size=16
        - --zmq-endpoint=tcp://food-review-endpoint-picker.default.svc.cluster.local:5557
        - --event-batch-size=16
        - --data-parallel-size=1
        ports:
        - containerPort: 8200
          name: http
```

### STT Backend

```yaml
metadata:
  name: stt-vllm-sim
spec:
  template:
    metadata:
      labels:
        app: food-review-inference-pool
        llm-d.ai/model-arch: encoder-decoder-stt
        llm-d.ai/inferenceServing: "true"
    spec:
      containers:
      - name: vllm
        args:
        - --model=whisper-1
        # ... same structure as TTS
```

### Image Generation Backend

```yaml
metadata:
  name: image-gen-vllm-sim
spec:
  template:
    metadata:
      labels:
        app: food-review-inference-pool
        llm-d.ai/model-arch: diffusion
        llm-d.ai/inferenceServing: "true"
    spec:
      containers:
      - name: vllm
        args:
        - --model=dall-e-3
        # ... same structure
```

### LLM Backend

```yaml
metadata:
  name: llm-vllm-sim
spec:
  template:
    metadata:
      labels:
        app: food-review-inference-pool
        llm-d.ai/model-arch: autoregressive-llm
        llm-d.ai/inferenceServing: "true"
    spec:
      containers:
      - name: vllm
        args:
        - --model=llama-3
        # ... same structure
```

### Deploy Backends

```bash
kubectl apply -f multimodal-vllm-sims.yaml
```

### Verify Deployment

```bash
# Check all pods are running
kubectl get pods -l llm-d.ai/inferenceServing=true

# Expected output:
# NAME                              READY   STATUS
# tts-vllm-sim-...                  2/2     Running
# stt-vllm-sim-...                  2/2     Running
# image-gen-vllm-sim-...            2/2     Running
# llm-vllm-sim-...                  2/2     Running
```

## EPP Configuration

### Modality Filter Configuration

**File:** `epp-config-multimodal.yaml`

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: epp-config
  namespace: default
data:
  epp-config.yaml: |
    apiVersion: inference.networking.x-k8s.io/v1alpha1
    kind: EndpointPickerConfig
    plugins:
    - type: modality-filter
    - type: prefix-cache-scorer
      parameters:
        hashBlockSize: 5
        maxPrefixBlocksToMatch: 256
        lruCapacityPerServer: 31250
    - type: decode-filter
    - type: max-score-picker
    - type: single-profile-handler
    schedulingProfiles:
    - name: default
      plugins:
      - pluginRef: modality-filter
      - pluginRef: decode-filter
      - pluginRef: max-score-picker
      - pluginRef: prefix-cache-scorer
        weight: 2
```

### Modality Filter Routing Logic

The modality filter maps endpoint paths to compatible model architectures:

| Endpoint Path | Compatible Architectures |
|---------------|-------------------------|
| `/v1/audio/speech` | `autoregressive-tts`, `omni-llm` |
| `/v1/audio/transcriptions` | `encoder-decoder-stt` |
| `/v1/images/generations` | `diffusion` |
| `/v1/chat/completions` | `autoregressive-llm`, `omni-llm` |

**How it works:**
1. EPP receives request from Envoy via gRPC ext-proc
2. Request parser extracts endpoint path
3. Modality filter determines compatible architectures for that path
4. Filters pod list to only those with matching `llm-d.ai/model-arch` label
5. Other plugins (scorers, pickers) select best pod from filtered list
6. EPP returns selected pod address to Envoy

## Testing

### Test TTS Endpoint

```bash
curl -X POST http://localhost:8080/v1/audio/speech \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "tts-1",
    "input": "Hello, this is a test of the text to speech endpoint.",
    "voice": "alloy"
  }'
```

**Expected:** Request routes to TTS pod (autoregressive-tts)

### Test STT Endpoint

```bash
curl -X POST http://localhost:8080/v1/audio/transcriptions \
  -H 'Content-Type: application/json' \
  -d '{
    "file": "audio.wav",
    "model": "whisper-1"
  }'
```

**Expected:** Request routes to STT pod (encoder-decoder-stt)

### Test Image Generation Endpoint

```bash
curl -X POST http://localhost:8080/v1/images/generations \
  -H 'Content-Type: application/json' \
  -d '{
    "prompt": "A beautiful sunset over mountains",
    "model": "dall-e-3",
    "n": 1,
    "size": "1024x1024"
  }'
```

**Expected:** Request routes to Image pod (diffusion)

### Test LLM Endpoint (baseline)

```bash
curl -X POST http://localhost:8080/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "llama-3",
    "messages": [
      {"role": "user", "content": "What is the capital of France?"}
    ]
  }'
```

**Expected:** Request routes to LLM pod (autoregressive-llm)

## Verification

### Check EPP Logs

```bash
kubectl logs deployment/food-review-endpoint-picker -c epp --tail=100 | grep "modality-filter"
```

**Expected output:**
```json
{"msg":"Running filter plugin","plugin":"modality-filter/modality-filter"}
{"msg":"Completed running filter plugin successfully",
 "endpoints":[{"PodName":"tts-vllm-sim-...","llm-d.ai/model-arch":"autoregressive-tts"}]}
```

### Check Pod Discovery

```bash
kubectl logs deployment/food-review-endpoint-picker -c epp --tail=200 | grep "Before running filter plugins" -A1
```

**Expected:** All 4 pods listed with their labels:
- tts-vllm-sim (autoregressive-tts)
- stt-vllm-sim (encoder-decoder-stt)
- image-gen-vllm-sim (diffusion)
- llm-vllm-sim (autoregressive-llm)

### Verify Filtering

For a TTS request, EPP should:
1. Start with 4 candidate pods
2. Modality filter reduces to 1 pod (autoregressive-tts)
3. Return tts-vllm-sim pod address

**Check logs:**
```bash
kubectl logs deployment/food-review-endpoint-picker -c epp --tail=500 | \
  grep -E "Before running filter|Completed running filter" | tail -10
```

## Test Scripts

### Automated Testing

**File:** `test-endpoints.sh` (in llm-d repo)

Tests all endpoints with color-coded output:

```bash
#!/bin/bash
./test-endpoints.sh
```

**Output:**
```
========================================
Testing LLM-D Inference Scheduler Endpoints
========================================

Testing: TTS Request
✓ Success

Testing: STT Request
✓ Success

Testing: Image Generation Request
✓ Success

Testing: Chat Completions
✓ Success
```

### Manual Port-Forward Testing

```bash
# Port-forward EPP/Gateway
kubectl port-forward svc/inference-gateway-istio 8080:80 &

# Test endpoint
curl -X POST http://localhost:8080/v1/audio/speech \
  -H 'Content-Type: application/json' \
  -d '{"model":"tts-1","input":"hello"}'
  
# Check logs
kubectl logs deployment/food-review-endpoint-picker -c epp --tail=20
```

## Troubleshooting

### Issue: EPP Not Discovering Pods

**Symptom:** EPP logs show "Pod removed or not added"

**Causes:**
1. Missing `app: food-review-inference-pool` label
2. Missing `llm-d.ai/inferenceServing: "true"` label
3. vllm container not running or returning 503

**Fix:**
```bash
# Check pod labels
kubectl get pods -l llm-d.ai/inferenceServing=true --show-labels

# Check pod readiness
kubectl get pods -l llm-d.ai/inferenceServing=true

# Should show 2/2 READY for each pod
```

### Issue: Wrong Pod Selected

**Symptom:** TTS request goes to LLM pod

**Cause:** Pod missing or has wrong `llm-d.ai/model-arch` label

**Fix:**
```bash
# Verify labels
kubectl get pod tts-vllm-sim-<pod-id> -o jsonpath='{.metadata.labels}' | jq .

# Expected:
{
  "app": "food-review-inference-pool",
  "llm-d.ai/model-arch": "autoregressive-tts",
  "llm-d.ai/inferenceServing": "true"
}
```

### Issue: 404 from Backend

**Symptom:** Request reaches pod but returns 404

**Cause:** vllm-sim is a mock - doesn't implement all endpoints

**Expected:** This is normal. Real vLLM backends will handle requests.

### Issue: Timeout/Connection Refused

**Symptom:** Request hangs or times out

**Causes:**
1. Envoy Gateway not running
2. Port-forward not active
3. Network policy blocking traffic

**Fix:**
```bash
# Check Envoy Gateway
kubectl get svc inference-gateway-istio

# Check port-forward
ps aux | grep port-forward

# Restart port-forward
pkill -f port-forward
kubectl port-forward svc/inference-gateway-istio 8080:80 &
```

## Cleanup

```bash
# Delete cluster
kind delete cluster --name llm-d-inference-scheduler-dev

# Or just delete resources
kubectl delete -f multimodal-vllm-sims.yaml
kubectl delete -f epp-config-multimodal.yaml
```

## Summary

### What Works

✅ **EPP Request Parser**
- Recognizes 4 new endpoint paths
- Bypasses strict validation for multimodal requests
- Extracts request metadata correctly

✅ **Modality Filter**
- Routes TTS requests → autoregressive-tts pods
- Routes STT requests → encoder-decoder-stt pods
- Routes image requests → diffusion pods
- Routes LLM requests → autoregressive-llm pods

✅ **Pod Discovery**
- EPP discovers all 4 backend pods
- Polls metrics via ZMQ
- Maintains endpoint pool with labels

✅ **Full Request Chain**
- Client → Envoy → EPP → Backend → Response
- Verified in logs at each layer

### Deployment Files

Located in `llm-d` repository:
- `multimodal-vllm-sims.yaml` - Backend pod deployments
- `epp-config-multimodal.yaml` - EPP configuration
- `kind-cluster-config.yaml` - Kind cluster config
- `setup-kind-cluster.sh` - Automated setup
- `test-endpoints.sh` - Test script

### Code Changes

Located in `llm-d-router` repository:
- `pkg/epp/framework/plugins/requesthandling/parsers/openai/openai.go` - Parser updates
- `pkg/common/multimodal.go` - Shared constants
- `pkg/sidecar/proxy/proxy.go` - Sidecar handler registration

**PR:** https://github.com/rh-waterford-et/llm-d-router/pull/5

## Next Steps

- ✅ Coordinator integration (completed separately)
- Deploy real vLLM backends (replace sims)
- Add render service for multimodal preprocessing
- Configure disagg-profile-handler for phase-based routing
- Performance testing with actual models
