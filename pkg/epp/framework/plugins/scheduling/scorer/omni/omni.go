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
	"encoding/json"
	"fmt"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/llm-d/llm-d-router/pkg/common"
	logutil "github.com/llm-d/llm-d-router/pkg/common/observability/logging"
	fwkplugin "github.com/llm-d/llm-d-router/pkg/epp/framework/interface/plugin"
	fwksched "github.com/llm-d/llm-d-router/pkg/epp/framework/interface/scheduling"
	"github.com/llm-d/llm-d-router/pkg/epp/framework/plugins/datalayer/extractor/metrics"
)

const (
	// OmniLLMScorerType is the type name used in EPP configuration.
	OmniLLMScorerType = "omni-llm-scorer"

	// QueueThresholdDefault is the default queue depth threshold.
	// Omni models are autoregressive and share the same KV cache budget
	// across text, audio, and vision tokens, so we use the same default
	// as the standard load-aware scorer.
	QueueThresholdDefault = 128

	// Scoring tier base values. Tiers are spaced so that the queue penalty
	// (max 0.2) can never cause a higher tier to drop below a lower tier.
	//
	//   model-active range  : [0.8, 1.0]
	//   model-loading range : [0.55, 0.75]
	//   arch-only range     : [0.3, 0.5]
	//   incompatible        : 0.0
	scoreArchAndModelMatch   = 1.0  // Architecture-compatible AND model actively serving.
	scoreModelLoading        = 0.75 // Architecture-compatible AND model queued to load.
	scoreArchOnlyMatch       = 0.5  // Architecture-compatible BUT different model loaded.
	scoreNoMatch             = 0.0  // Incompatible architecture (or unlabelled).

	// queuePenaltyWeight controls the maximum fraction of the base score
	// that queue depth can subtract. 0.2 keeps all tier ranges non-overlapping.
	queuePenaltyWeight = 0.2
)

type omniScorerParameters struct {
	QueueThreshold int `json:"queueThreshold"`
}

// compile-time type assertion
var _ fwksched.Scorer = &OmniLLMScorer{}

// OmniLLMScorerFactory defines the factory function for the OmniLLMScorer.
func OmniLLMScorerFactory(name string, params *json.Decoder, handle fwkplugin.Handle) (fwkplugin.Plugin, error) {
	parameters := omniScorerParameters{QueueThreshold: QueueThresholdDefault}
	if params != nil {
		if err := params.Decode(&parameters); err != nil {
			return nil, fmt.Errorf("failed to parse the parameters of the '%s' scorer - %w", OmniLLMScorerType, err)
		}
	}
	return NewOmniLLMScorer(handle.Context(), parameters.QueueThreshold).WithName(name), nil
}

// OmniLLMScorer scores endpoints that serve omni-capable models (e.g. Qwen-Omni).
//
// Omni models handle text, audio, and image generation through a single
// autoregressive architecture. The scorer uses a three-tier approach:
//
//  1. Model actively serving — the requested model is already loaded and hot.
//  2. Model loading — the requested model is queued to load (WaitingModels).
//  3. Architecture-only — the endpoint's architecture is compatible but it
//     serves a different model entirely.
//
// Within each tier, queue depth acts as a tiebreaker: busier pods score
// lower, but never below the next tier down. This preserves strict tier
// ordering while distributing load across equally-suitable endpoints.
type OmniLLMScorer struct {
	typedName      fwkplugin.TypedName
	queueThreshold float64
}

// NewOmniLLMScorer creates a new OmniLLMScorer instance.
func NewOmniLLMScorer(ctx context.Context, queueThreshold int) *OmniLLMScorer {
	if queueThreshold <= 0 {
		queueThreshold = QueueThresholdDefault
		log.FromContext(ctx).V(logutil.DEFAULT).Info(fmt.Sprintf(
			"OmniLLM queueThreshold %d should be positive, using default %d",
			queueThreshold, QueueThresholdDefault))
	}
	return &OmniLLMScorer{
		typedName:      fwkplugin.TypedName{Type: OmniLLMScorerType},
		queueThreshold: float64(queueThreshold),
	}
}

// WithName sets the name of the plugin.
func (s *OmniLLMScorer) WithName(name string) *OmniLLMScorer {
	s.typedName.Name = name
	return s
}

// TypedName returns the typed name of the plugin.
func (s *OmniLLMScorer) TypedName() fwkplugin.TypedName {
	return s.typedName
}

// Category returns Affinity because the dominant scoring signal is model-name
// and architecture compatibility, not load distribution.
func (s *OmniLLMScorer) Category() fwksched.ScorerCategory {
	return fwksched.Affinity
}

// Consumes returns the data consumed by this scorer.
func (s *OmniLLMScorer) Consumes() map[string]any {
	return map[string]any{
		metrics.ActiveModelsKey:     map[string]int{},
		metrics.WaitingModelsKey:    map[string]int{},
		metrics.WaitingQueueSizeKey: int(0),
	}
}

// Score scores each candidate endpoint based on architecture compatibility,
// model-name affinity, and queue depth.
//
// The request path (from the Envoy :path header) determines whether this is
// a multimodal request. For multimodal paths (e.g. /v1/audio/speech), the
// scorer checks the endpoint's model-arch label against the path's compatible
// architectures. For standard text paths (e.g. /v1/chat/completions), the
// architecture check is skipped and all endpoints are scored on model-name
// affinity + queue depth only.
//
// Scoring tiers:
//
//	1.0  — architecture-compatible AND model actively serving
//	0.75 — architecture-compatible AND model queued to load
//	0.5  — architecture-compatible BUT different model loaded
//	0.0  — incompatible architecture or missing label
//
// Queue depth penalty within a tier:
//
//	penalty = min(waitingQueue / threshold, 1.0) * 0.2
//	finalScore = max(baseScore - penalty, 0.0)
func (s *OmniLLMScorer) Score(ctx context.Context, request *fwksched.InferenceRequest, endpoints []fwksched.Endpoint) map[fwksched.Endpoint]float64 {
	scores := make(map[fwksched.Endpoint]float64, len(endpoints))

	path := requestPath(request)
	validArchs := archSetForPath(path)
	isMultimodal := validArchs != nil

	allZero := true
	for _, ep := range endpoints {
		base := s.baseScore(ep, request.TargetModel, validArchs, isMultimodal)
		penalty := s.queuePenalty(ep)
		score := base - penalty
		if score < 0 {
			score = 0
		}
		scores[ep] = score
		if score > 0 {
			allZero = false
		}
	}

	if allZero && len(endpoints) > 0 {
		log.FromContext(ctx).V(logutil.DEFAULT).Info(
			"omni-llm-scorer: all endpoints scored 0.0 — possible misconfiguration, "+
				"check that modality-filter is configured or endpoints have correct model-arch labels",
			"path", path, "targetModel", request.TargetModel, "endpointCount", len(endpoints))
	}

	return scores
}

// baseScore determines the tier score for an endpoint.
func (s *OmniLLMScorer) baseScore(ep fwksched.Endpoint, targetModel string, validArchs map[string]struct{}, isMultimodal bool) float64 {
	if isMultimodal {
		md := ep.GetMetadata()
		if md == nil {
			return scoreNoMatch
		}
		archLabel := md.Labels[common.ModelArchLabel]
		if _, ok := validArchs[archLabel]; !ok {
			return scoreNoMatch
		}
	}

	switch modelAffinity(ep, targetModel) {
	case affinityActive:
		return scoreArchAndModelMatch
	case affinityLoading:
		return scoreModelLoading
	default:
		return scoreArchOnlyMatch
	}
}

// queuePenalty returns a penalty in [0, queuePenaltyWeight] based on the
// endpoint's waiting queue depth relative to the configured threshold.
func (s *OmniLLMScorer) queuePenalty(ep fwksched.Endpoint) float64 {
	m := ep.GetMetrics()
	if m == nil {
		return 0
	}
	queueDepth := float64(m.WaitingQueueSize)
	if queueDepth <= 0 {
		return 0
	}
	ratio := queueDepth / s.queueThreshold
	if ratio > 1.0 {
		ratio = 1.0
	}
	return ratio * queuePenaltyWeight
}

type modelAffinityLevel int

const (
	affinityNone    modelAffinityLevel = iota // model not present on this endpoint
	affinityLoading                           // model queued to load (in WaitingModels)
	affinityActive                            // model actively serving (in ActiveModels)
)

// modelAffinity returns the affinity level between the endpoint and the target model.
func modelAffinity(ep fwksched.Endpoint, targetModel string) modelAffinityLevel {
	if targetModel == "" {
		return affinityNone
	}
	m := ep.GetMetrics()
	if m == nil {
		return affinityNone
	}
	if _, active := m.ActiveModels[targetModel]; active {
		return affinityActive
	}
	if _, waiting := m.WaitingModels[targetModel]; waiting {
		return affinityLoading
	}
	return affinityNone
}

// requestPath extracts and normalises the request path from the :path header.
func requestPath(request *fwksched.InferenceRequest) string {
	if request == nil {
		return ""
	}
	path := request.Headers[common.EnvoyPathHeader]
	if idx := strings.IndexByte(path, '?'); idx >= 0 {
		path = path[:idx]
	}
	return path
}

// archSetForPath returns the set of compatible architectures for the given
// API path, or nil if the path is not a known multimodal endpoint.
func archSetForPath(path string) map[string]struct{} {
	archs, known := common.PathToModelArch[path]
	if !known {
		return nil
	}
	set := make(map[string]struct{}, len(archs))
	for _, a := range archs {
		set[a] = struct{}{}
	}
	return set
}
