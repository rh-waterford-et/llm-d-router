/*
Copyright 2025 The llm-d Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package omni

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/llm-d/llm-d-router/pkg/common"
	fwkdl "github.com/llm-d/llm-d-router/pkg/epp/framework/interface/datalayer"
	fwksched "github.com/llm-d/llm-d-router/pkg/epp/framework/interface/scheduling"
)

func newRequest(path, targetModel string) *fwksched.InferenceRequest {
	return &fwksched.InferenceRequest{
		TargetModel: targetModel,
		Headers:     map[string]string{common.EnvoyPathHeader: path},
	}
}

func omniEndpoint(modelName string, queueSize int) fwksched.Endpoint {
	return fwksched.NewEndpoint(
		&fwkdl.EndpointMetadata{
			Labels: map[string]string{common.ModelArchLabel: common.ModelArchOmniLLM},
		},
		&fwkdl.Metrics{
			ActiveModels:     map[string]int{modelName: 1},
			WaitingModels:    map[string]int{},
			WaitingQueueSize: queueSize,
		},
		nil,
	)
}

func omniEndpointLoading(modelName string, queueSize int) fwksched.Endpoint {
	return fwksched.NewEndpoint(
		&fwkdl.EndpointMetadata{
			Labels: map[string]string{common.ModelArchLabel: common.ModelArchOmniLLM},
		},
		&fwkdl.Metrics{
			ActiveModels:     map[string]int{"some-other-model": 1},
			WaitingModels:    map[string]int{modelName: 1},
			WaitingQueueSize: queueSize,
		},
		nil,
	)
}

func diffusionEndpoint(modelName string, queueSize int) fwksched.Endpoint {
	return fwksched.NewEndpoint(
		&fwkdl.EndpointMetadata{
			Labels: map[string]string{common.ModelArchLabel: common.ModelArchDiffusion},
		},
		&fwkdl.Metrics{
			ActiveModels:     map[string]int{modelName: 1},
			WaitingModels:    map[string]int{},
			WaitingQueueSize: queueSize,
		},
		nil,
	)
}

func unlabelledEndpoint(modelName string, queueSize int) fwksched.Endpoint {
	return fwksched.NewEndpoint(
		&fwkdl.EndpointMetadata{Labels: map[string]string{}},
		&fwkdl.Metrics{
			ActiveModels:     map[string]int{modelName: 1},
			WaitingModels:    map[string]int{},
			WaitingQueueSize: queueSize,
		},
		nil,
	)
}

// --- Multimodal request tests (audio/speech path) ---

func TestOmniScorer_MultimodalArchAndModelMatch(t *testing.T) {
	s := NewOmniLLMScorer(context.Background(), QueueThresholdDefault)
	ep := omniEndpoint("qwen-omni", 0)
	req := newRequest(common.AudioSpeechPath, "qwen-omni")

	scores := s.Score(context.Background(), req, []fwksched.Endpoint{ep})

	assert.InDelta(t, scoreArchAndModelMatch, scores[ep], 0.01,
		"omni pod serving requested model on multimodal path should score 1.0")
}

func TestOmniScorer_MultimodalArchMatchModelMismatch(t *testing.T) {
	s := NewOmniLLMScorer(context.Background(), QueueThresholdDefault)
	ep := omniEndpoint("other-omni-model", 0)
	req := newRequest(common.AudioSpeechPath, "qwen-omni")

	scores := s.Score(context.Background(), req, []fwksched.Endpoint{ep})

	assert.InDelta(t, scoreArchOnlyMatch, scores[ep], 0.01,
		"omni pod with different model on multimodal path should score 0.5")
}

func TestOmniScorer_MultimodalIncompatibleArch(t *testing.T) {
	s := NewOmniLLMScorer(context.Background(), QueueThresholdDefault)
	ep := diffusionEndpoint("stable-diffusion", 0)
	req := newRequest(common.AudioSpeechPath, "qwen-omni")

	scores := s.Score(context.Background(), req, []fwksched.Endpoint{ep})

	assert.InDelta(t, scoreNoMatch, scores[ep], 0.01,
		"diffusion pod on audio/speech path should score 0.0")
}

func TestOmniScorer_MultimodalNoLabel(t *testing.T) {
	s := NewOmniLLMScorer(context.Background(), QueueThresholdDefault)
	ep := unlabelledEndpoint("qwen-omni", 0)
	req := newRequest(common.AudioSpeechPath, "qwen-omni")

	scores := s.Score(context.Background(), req, []fwksched.Endpoint{ep})

	assert.InDelta(t, scoreNoMatch, scores[ep], 0.01,
		"unlabelled pod on multimodal path should score 0.0")
}

// --- WaitingModels (model loading) tier ---

func TestOmniScorer_MultimodalModelLoading(t *testing.T) {
	s := NewOmniLLMScorer(context.Background(), QueueThresholdDefault)
	ep := omniEndpointLoading("qwen-omni", 0)
	req := newRequest(common.AudioSpeechPath, "qwen-omni")

	scores := s.Score(context.Background(), req, []fwksched.Endpoint{ep})

	assert.InDelta(t, scoreModelLoading, scores[ep], 0.01,
		"omni pod loading requested model should score 0.75")
}

func TestOmniScorer_LoadingBetweenActiveAndArchOnly(t *testing.T) {
	s := NewOmniLLMScorer(context.Background(), QueueThresholdDefault)
	active := omniEndpoint("qwen-omni", 0)
	loading := omniEndpointLoading("qwen-omni", 0)
	archOnly := omniEndpoint("other-model", 0)
	req := newRequest(common.AudioSpeechPath, "qwen-omni")

	scores := s.Score(context.Background(), req, []fwksched.Endpoint{active, loading, archOnly})

	assert.Greater(t, scores[active], scores[loading],
		"active model should beat loading model")
	assert.Greater(t, scores[loading], scores[archOnly],
		"loading model should beat arch-only match")
}

// --- Text request tests (chat/completions path) ---

func TestOmniScorer_TextPathModelMatch(t *testing.T) {
	s := NewOmniLLMScorer(context.Background(), QueueThresholdDefault)
	ep := omniEndpoint("qwen-omni", 0)
	req := newRequest("/v1/chat/completions", "qwen-omni")

	scores := s.Score(context.Background(), req, []fwksched.Endpoint{ep})

	assert.InDelta(t, scoreArchAndModelMatch, scores[ep], 0.01,
		"on text path, model-match should score 1.0 regardless of arch label")
}

func TestOmniScorer_TextPathModelMismatch(t *testing.T) {
	s := NewOmniLLMScorer(context.Background(), QueueThresholdDefault)
	ep := omniEndpoint("other-model", 0)
	req := newRequest("/v1/chat/completions", "qwen-omni")

	scores := s.Score(context.Background(), req, []fwksched.Endpoint{ep})

	assert.InDelta(t, scoreArchOnlyMatch, scores[ep], 0.01,
		"on text path, model-mismatch should score 0.5 (arch check skipped)")
}

// --- Queue depth tiebreaking ---

func TestOmniScorer_QueuePenaltyWithinTier(t *testing.T) {
	s := NewOmniLLMScorer(context.Background(), QueueThresholdDefault)
	idle := omniEndpoint("qwen-omni", 0)
	busy := omniEndpoint("qwen-omni", 64)
	req := newRequest(common.AudioSpeechPath, "qwen-omni")

	scores := s.Score(context.Background(), req, []fwksched.Endpoint{idle, busy})

	assert.Greater(t, scores[idle], scores[busy],
		"idle pod should score higher than busy pod in the same tier")
	assert.Greater(t, scores[busy], scoreArchOnlyMatch,
		"busy model-match pod should still score above arch-only tier")
}

func TestOmniScorer_TierOrderPreservedUnderLoad(t *testing.T) {
	s := NewOmniLLMScorer(context.Background(), QueueThresholdDefault)
	// Model-match pod at full queue capacity
	busyMatch := omniEndpoint("qwen-omni", QueueThresholdDefault)
	// Arch-only pod completely idle
	idleMismatch := omniEndpoint("other-model", 0)
	req := newRequest(common.AudioSpeechPath, "qwen-omni")

	scores := s.Score(context.Background(), req, []fwksched.Endpoint{busyMatch, idleMismatch})

	// busyMatch: 1.0 - 0.2 = 0.8, idleMismatch: 0.5 - 0.0 = 0.5
	assert.Greater(t, scores[busyMatch], scores[idleMismatch],
		"saturated model-match pod (0.8) must still beat idle arch-only pod (0.5)")
}

func TestOmniScorer_AllTiersPreservedUnderLoad(t *testing.T) {
	s := NewOmniLLMScorer(context.Background(), QueueThresholdDefault)
	busyActive := omniEndpoint("qwen-omni", QueueThresholdDefault)
	busyLoading := omniEndpointLoading("qwen-omni", QueueThresholdDefault)
	busyArchOnly := omniEndpoint("other-model", QueueThresholdDefault)
	req := newRequest(common.AudioSpeechPath, "qwen-omni")

	scores := s.Score(context.Background(), req, []fwksched.Endpoint{busyActive, busyLoading, busyArchOnly})

	// All at max queue: active 1.0-0.2=0.8, loading 0.75-0.2=0.55, archOnly 0.5-0.2=0.3
	assert.Greater(t, scores[busyActive], scores[busyLoading],
		"saturated active (0.8) must beat saturated loading (0.55)")
	assert.Greater(t, scores[busyLoading], scores[busyArchOnly],
		"saturated loading (0.55) must beat saturated arch-only (0.3)")
}

func TestOmniScorer_QueueOverThresholdClamps(t *testing.T) {
	s := NewOmniLLMScorer(context.Background(), QueueThresholdDefault)
	overloaded := omniEndpoint("qwen-omni", QueueThresholdDefault*2)
	atThreshold := omniEndpoint("qwen-omni", QueueThresholdDefault)
	req := newRequest(common.AudioSpeechPath, "qwen-omni")

	scores := s.Score(context.Background(), req, []fwksched.Endpoint{overloaded, atThreshold})

	assert.InDelta(t, scores[overloaded], scores[atThreshold], 0.01,
		"queue beyond threshold should clamp to maximum penalty")
}

// --- Factory ---

func TestOmniScorer_FactoryDefaultThreshold(t *testing.T) {
	s := NewOmniLLMScorer(context.Background(), QueueThresholdDefault)
	assert.Equal(t, float64(QueueThresholdDefault), s.queueThreshold)
	assert.Equal(t, OmniLLMScorerType, s.typedName.Type)
}

func TestOmniScorer_FactoryNegativeThresholdUsesDefault(t *testing.T) {
	s := NewOmniLLMScorer(context.Background(), -1)
	assert.Equal(t, float64(QueueThresholdDefault), s.queueThreshold,
		"negative threshold should fallback to default")
}

func TestOmniScorer_Category(t *testing.T) {
	s := NewOmniLLMScorer(context.Background(), QueueThresholdDefault)
	assert.Equal(t, fwksched.Affinity, s.Category())
}

// --- Edge cases ---

func TestOmniScorer_EmptyTargetModel(t *testing.T) {
	s := NewOmniLLMScorer(context.Background(), QueueThresholdDefault)
	ep := omniEndpoint("qwen-omni", 0)
	req := newRequest(common.AudioSpeechPath, "")

	scores := s.Score(context.Background(), req, []fwksched.Endpoint{ep})

	assert.InDelta(t, scoreArchOnlyMatch, scores[ep], 0.01,
		"empty target model should fallback to arch-only match")
}

func TestOmniScorer_PathWithQueryString(t *testing.T) {
	s := NewOmniLLMScorer(context.Background(), QueueThresholdDefault)
	ep := omniEndpoint("qwen-omni", 0)
	req := newRequest(common.AudioSpeechPath+"?format=wav", "qwen-omni")

	scores := s.Score(context.Background(), req, []fwksched.Endpoint{ep})

	assert.InDelta(t, scoreArchAndModelMatch, scores[ep], 0.01,
		"query string should be stripped before path lookup")
}

func TestOmniScorer_NilEndpointMetadata(t *testing.T) {
	s := NewOmniLLMScorer(context.Background(), QueueThresholdDefault)
	ep := fwksched.NewEndpoint(nil, &fwkdl.Metrics{WaitingQueueSize: 0}, nil)
	req := newRequest(common.AudioSpeechPath, "qwen-omni")

	scores := s.Score(context.Background(), req, []fwksched.Endpoint{ep})

	assert.InDelta(t, scoreNoMatch, scores[ep], 0.01,
		"nil metadata on multimodal path should score 0.0")
}

func TestOmniScorer_NilMetrics(t *testing.T) {
	s := NewOmniLLMScorer(context.Background(), QueueThresholdDefault)
	ep := fwksched.NewEndpoint(
		&fwkdl.EndpointMetadata{
			Labels: map[string]string{common.ModelArchLabel: common.ModelArchOmniLLM},
		},
		nil, nil,
	)
	req := newRequest(common.AudioSpeechPath, "qwen-omni")

	scores := s.Score(context.Background(), req, []fwksched.Endpoint{ep})

	assert.InDelta(t, scoreArchOnlyMatch, scores[ep], 0.01,
		"nil metrics should fallback to arch-only match with no queue penalty")
}

func TestOmniScorer_EmptyEndpointList(t *testing.T) {
	s := NewOmniLLMScorer(context.Background(), QueueThresholdDefault)
	req := newRequest(common.AudioSpeechPath, "qwen-omni")

	scores := s.Score(context.Background(), req, []fwksched.Endpoint{})

	assert.Empty(t, scores)
}
