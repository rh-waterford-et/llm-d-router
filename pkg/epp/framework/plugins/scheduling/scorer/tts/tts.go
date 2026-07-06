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

package tts

import (
	"context"
	"encoding/json"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/log"

	logutil "github.com/llm-d/llm-d-router/pkg/common/observability/logging"
	fwkplugin "github.com/llm-d/llm-d-router/pkg/epp/framework/interface/plugin"
	fwksched "github.com/llm-d/llm-d-router/pkg/epp/framework/interface/scheduling"
	"github.com/llm-d/llm-d-router/pkg/epp/framework/plugins/datalayer/extractor/metrics"
)

const (
	// TTSScorerType is the type of the TTS scorer plugin.
	TTSScorerType = "tts-scorer"

	// TTSQueueThresholdDefault is the default queue threshold for TTS workloads.
	// TTS workloads are latency-sensitive so the threshold is lower than text.
	TTSQueueThresholdDefault = 64
)

type ttsScorerParameters struct {
	QueueThreshold int `json:"queueThreshold"`
}

var _ fwksched.Scorer = &TTSScorer{}

// TTSScorerFactory defines the factory function for the TTSScorer.
func TTSScorerFactory(name string, params *json.Decoder, handle fwkplugin.Handle) (fwkplugin.Plugin, error) {
	parameters := ttsScorerParameters{QueueThreshold: TTSQueueThresholdDefault}
	if params != nil {
		if err := params.Decode(&parameters); err != nil {
			return nil, fmt.Errorf("failed to parse the parameters of the '%s' scorer - %w", TTSScorerType, err)
		}
	}
	return NewTTSScorer(handle.Context(), parameters.QueueThreshold).WithName(name), nil
}

// TTSScorer scores endpoints for text-to-speech workloads.
// Primary signal: queue depth (TTS is latency-sensitive, prefer least-loaded).
// Secondary signal: GPU memory pressure.
type TTSScorer struct {
	typedName      fwkplugin.TypedName
	queueThreshold float64
}

// NewTTSScorer creates a new TTS scorer instance.
func NewTTSScorer(ctx context.Context, queueThreshold int) *TTSScorer {
	if queueThreshold <= 0 {
		queueThreshold = TTSQueueThresholdDefault
		log.FromContext(ctx).V(logutil.DEFAULT).Info(fmt.Sprintf(
			"TTS queueThreshold %d should be positive, using default %d",
			queueThreshold, TTSQueueThresholdDefault))
	}
	return &TTSScorer{
		typedName:      fwkplugin.TypedName{Type: TTSScorerType},
		queueThreshold: float64(queueThreshold),
	}
}

// WithName sets the name of the plugin.
func (s *TTSScorer) WithName(name string) *TTSScorer {
	s.typedName.Name = name
	return s
}

// TypedName returns the typed name of the plugin.
func (s *TTSScorer) TypedName() fwkplugin.TypedName {
	return s.typedName
}

// Category returns the preference the scorer applies when scoring candidate endpoints.
func (s *TTSScorer) Category() fwksched.ScorerCategory {
	return fwksched.Distribution
}

// Consumes returns the data consumed by this scorer.
func (s *TTSScorer) Consumes() map[string]any {
	return map[string]any{
		metrics.WaitingQueueSizeKey:    int(0),
		metrics.KVCacheUsagePercentKey: float64(0),
	}
}

// Score scores endpoints for TTS workloads. Endpoints with lower queue depth
// get higher scores since TTS is latency-sensitive and dominated by the decode
// phase. The scoring formula weights queue depth heavily (70%) with a
// secondary consideration for GPU memory utilisation (30%).
func (s *TTSScorer) Score(_ context.Context, _ *fwksched.InferenceRequest, endpoints []fwksched.Endpoint) map[fwksched.Endpoint]float64 {
	scores := make(map[fwksched.Endpoint]float64, len(endpoints))

	for _, ep := range endpoints {
		m := ep.GetMetrics()
		queueDepth := float64(m.WaitingQueueSize)

		var queueScore float64
		if queueDepth >= s.queueThreshold {
			queueScore = 0.0
		} else {
			queueScore = 1.0 - (queueDepth / s.queueThreshold)
		}

		gpuMemScore := 1.0 - m.KVCacheUsagePercent

		scores[ep] = 0.7*queueScore + 0.3*gpuMemScore
	}
	return scores
}
