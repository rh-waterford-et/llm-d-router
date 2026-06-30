#!/bin/bash
set -e

CLUSTER_NAME="llm-d-inference-scheduler-dev"

echo "========================================="
echo "LLM-D Multimodal Routing Setup (PR #5)"
echo "========================================="

# Delete existing cluster
if kind get clusters 2>/dev/null | grep -q "$CLUSTER_NAME"; then
  echo "Deleting existing cluster..."
  kind delete cluster --name "$CLUSTER_NAME"
fi

# Create cluster
echo "Creating kind cluster..."
kind create cluster --config kind-cluster-config.yaml
kubectl wait --for=condition=Ready nodes --all --timeout=120s

# Load images
echo ""
echo "Loading Docker images..."
kind load docker-image ghcr.io/llm-d/llm-d-inference-scheduler:dev --name "$CLUSTER_NAME" || { echo "ERROR: EPP image not found - build with: make docker-build-epp"; exit 1; }
kind load docker-image ghcr.io/llm-d/llm-d-router-disagg-sidecar:dev --name "$CLUSTER_NAME" || { echo "ERROR: Sidecar image not found - build with: make docker-build-sidecar"; exit 1; }
echo "vllm-sim will be pulled by Kubernetes (ghcr.io/llm-d/llm-d-inference-sim:v0.8.2)"

# Deploy
echo ""
echo "Deploying components..."
kubectl apply -f inferencepool-crd.yaml
kubectl apply -f inference.networking.x-k8s.io_inferenceobjectives.yaml
kubectl apply -f epp-deployment.yaml
kubectl apply -f epp-config-multimodal.yaml
kubectl apply -f envoy-gateway.yaml
kubectl apply -f inferencepool.yaml
kubectl apply -f multimodal-vllm-sims.yaml

echo ""
echo "Waiting for pods..."
sleep 5
kubectl wait --for=condition=available deployment/food-review-endpoint-picker --timeout=120s || echo "EPP still starting..."
kubectl wait --for=condition=available deployment/inference-gateway --timeout=120s || echo "Gateway still starting..."
kubectl wait --for=condition=ready pod -l llm-d.ai/inferenceServing=true --timeout=120s || echo "Backends still starting..."

echo ""
echo "========================================="
echo "Deployment Complete"
echo "========================================="
kubectl get pods

echo ""
echo "Verify EPP discovered backends:"
echo "  kubectl logs deployment/food-review-endpoint-picker | grep 'Starting refresher'"
echo ""
echo "Test routing through gateway:"
echo "  kubectl port-forward svc/inference-gateway 8080:80 &"
echo "  curl -X POST http://localhost:8080/v1/audio/speech -H 'Content-Type: application/json' -d '{\"model\":\"tts-1\",\"input\":\"test\"}'"
echo ""
echo "Cleanup:"
echo "  kind delete cluster --name $CLUSTER_NAME"
