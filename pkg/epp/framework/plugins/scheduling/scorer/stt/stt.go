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

package stt

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
	// STTScorerType is the type of the STT scorer plugin.
	STTScorerType = "stt-scorer"

	// STTQueueThresholdDefault is the default queue threshold for STT workloads.
	STTQueueThresholdDefault = 96
)

type sttScorerParameters struct {
	QueueThreshold int `json:"queueThreshold"`
}

var _ fwksched.Scorer = &STTScorer{}

// STTScorerFactory defines the factory function for the STTScorer.
func STTScorerFactory(name string, params *json.Decoder, handle fwkplugin.Handle) (fwkplugin.Plugin, error) {
	parameters := sttScorerParameters{QueueThreshold: STTQueueThresholdDefault}
	if params != nil {
		if err := params.Decode(&parameters); err != nil {
			return nil, fmt.Errorf("failed to parse the parameters of the '%s' scorer - %w", STTScorerType, err)
		}
	}
	return NewSTTScorer(handle.Context(), parameters.QueueThreshold).WithName(name), nil
}

// STTScorer scores endpoints for speech-to-text workloads (e.g. Whisper).
// Primary signal: queue depth (encoder batching is the key optimisation).
// Secondary signal: encoder batch headroom estimated via active request count.
type STTScorer struct {
	typedName      fwkplugin.TypedName
	queueThreshold float64
}

// NewSTTScorer creates a new STT scorer instance.
func NewSTTScorer(ctx context.Context, queueThreshold int) *STTScorer {
	if queueThreshold <= 0 {
		queueThreshold = STTQueueThresholdDefault
		log.FromContext(ctx).V(logutil.DEFAULT).Info(fmt.Sprintf(
			"STT queueThreshold %d should be positive, using default %d",
			queueThreshold, STTQueueThresholdDefault))
	}
	return &STTScorer{
		typedName:      fwkplugin.TypedName{Type: STTScorerType},
		queueThreshold: float64(queueThreshold),
	}
}

// WithName sets the name of the plugin.
func (s *STTScorer) WithName(name string) *STTScorer {
	s.typedName.Name = name
	return s
}

// TypedName returns the typed name of the plugin.
func (s *STTScorer) TypedName() fwkplugin.TypedName {
	return s.typedName
}

// Category returns the preference the scorer applies when scoring candidate endpoints.
func (s *STTScorer) Category() fwksched.ScorerCategory {
	return fwksched.Distribution
}

// Consumes returns the data consumed by this scorer.
func (s *STTScorer) Consumes() map[string]any {
	return map[string]any{
		metrics.WaitingQueueSizeKey:    int(0),
		metrics.RunningRequestsSizeKey: int(0),
	}
}

// Score scores endpoints for STT workloads. Encoder-decoder models like Whisper
// benefit most from encoder batching -- endpoints with lower queue depth have
// more headroom for batching incoming encoder work.
func (s *STTScorer) Score(_ context.Context, _ *fwksched.InferenceRequest, endpoints []fwksched.Endpoint) map[fwksched.Endpoint]float64 {
	scores := make(map[fwksched.Endpoint]float64, len(endpoints))

	for _, ep := range endpoints {
		m := ep.GetMetrics()
		queueDepth := float64(m.WaitingQueueSize)
		activeRequests := float64(m.RunningRequestsSize)

		var queueScore float64
		if queueDepth >= s.queueThreshold {
			queueScore = 0.0
		} else {
			queueScore = 1.0 - (queueDepth / s.queueThreshold)
		}

		var activeScore float64
		if activeRequests >= s.queueThreshold {
			activeScore = 0.0
		} else {
			activeScore = 1.0 - (activeRequests / s.queueThreshold)
		}

		// 60% queue depth, 40% active requests (encoder headroom proxy)
		scores[ep] = 0.6*queueScore + 0.4*activeScore
	}
	return scores
}
