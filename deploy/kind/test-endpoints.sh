#!/bin/bash

SCHEDULER_URL="http://localhost:8080"
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo "========================================="
echo "Testing LLM-D Inference Scheduler Endpoints"
echo "========================================="
echo ""

# Function to test an endpoint
test_endpoint() {
  local name="$1"
  local endpoint="$2"
  local method="$3"
  local data="$4"

  echo -e "${YELLOW}Testing: $name${NC}"
  echo "Endpoint: $method $SCHEDULER_URL$endpoint"
  echo "Request:"
  echo "$data" | jq . 2>/dev/null || echo "$data"
  echo ""

  if [ "$method" = "POST" ]; then
    response=$(curl -s -X POST "$SCHEDULER_URL$endpoint" \
      -H "Content-Type: application/json" \
      -d "$data" \
      -w "\nHTTP_CODE:%{http_code}")
  else
    response=$(curl -s -X "$method" "$SCHEDULER_URL$endpoint" \
      -w "\nHTTP_CODE:%{http_code}")
  fi

  http_code=$(echo "$response" | grep "HTTP_CODE:" | cut -d: -f2)
  body=$(echo "$response" | grep -v "HTTP_CODE:")

  echo "Response (HTTP $http_code):"
  echo "$body" | jq . 2>/dev/null || echo "$body"

  if [ "$http_code" -ge 200 ] && [ "$http_code" -lt 300 ]; then
    echo -e "${GREEN}✓ Success${NC}"
  else
    echo -e "${RED}✗ Failed${NC}"
  fi
  echo ""
  echo "---"
  echo ""
}

# Test 1: Health/Ready check
echo -e "${YELLOW}=== Basic Health Checks ===${NC}"
echo ""
test_endpoint "Health Check" "/healthz" "GET" ""
test_endpoint "Ready Check" "/readyz" "GET" ""

# Test 2: TTS endpoint (/v1/audio/speech)
echo -e "${YELLOW}=== PR #2: Text-to-Speech Endpoint ===${NC}"
echo ""
tts_request='{
  "model": "tts-1",
  "input": "Hello, this is a test of the text to speech endpoint.",
  "voice": "alloy"
}'
test_endpoint "TTS Request" "/v1/audio/speech" "POST" "$tts_request"

# Test 3: STT endpoint (/v1/audio/transcriptions)
echo -e "${YELLOW}=== PR #3: Speech-to-Text Endpoint ===${NC}"
echo ""
stt_request='{
  "file": "audio.wav",
  "model": "whisper-1"
}'
test_endpoint "STT Request" "/v1/audio/transcriptions" "POST" "$stt_request"

# Test 4: Image Generation endpoint (/v1/images/generations)
echo -e "${YELLOW}=== PR #4: Image Generation Endpoint ===${NC}"
echo ""
image_request='{
  "prompt": "A beautiful sunset over mountains",
  "model": "dall-e-3",
  "n": 1,
  "size": "1024x1024"
}'
test_endpoint "Image Generation Request" "/v1/images/generations" "POST" "$image_request"

# Test 5: Inference endpoint with routing hints (/v1/inference)
echo -e "${YELLOW}=== PR #5: Inference Endpoint with Routing Hints ===${NC}"
echo ""
inference_request='{
  "model": "llama-3",
  "messages": [
    {"role": "user", "content": "Hello, how are you?"}
  ],
  "routing_hints": {
    "preferred_backend": "vllm-llm",
    "priority": "high"
  }
}'
test_endpoint "Inference with Routing Hints" "/v1/inference" "POST" "$inference_request"

# Test 6: Standard chat completions endpoint (for comparison)
echo -e "${YELLOW}=== Standard Chat Completions (baseline) ===${NC}"
echo ""
chat_request='{
  "model": "llama-3",
  "messages": [
    {"role": "user", "content": "What is the capital of France?"}
  ]
}'
test_endpoint "Chat Completions" "/v1/chat/completions" "POST" "$chat_request"

echo "========================================="
echo "Testing complete!"
echo "========================================="
echo ""
echo "To view scheduler logs:"
echo "  kubectl logs -n llm-d-test deployment/inference-scheduler -f"
echo ""
echo "To check backend pod status:"
echo "  kubectl get pods -n llm-d-test"
echo ""
echo "To tear down the cluster:"
echo "  kind delete cluster --name llm-d-test"
echo "========================================="
