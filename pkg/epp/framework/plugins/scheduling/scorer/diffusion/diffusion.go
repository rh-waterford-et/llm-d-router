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

package diffusion

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
	// DiffusionScorerType is the type of the diffusion model scorer plugin.
	DiffusionScorerType = "diffusion-scorer"

	// DiffusionQueueThresholdDefault is the default queue threshold for diffusion workloads.
	DiffusionQueueThresholdDefault = 32
)

type diffusionScorerParameters struct {
	QueueThreshold int `json:"queueThreshold"`
}

var _ fwksched.Scorer = &DiffusionScorer{}

// DiffusionScorerFactory defines the factory function for the DiffusionScorer.
func DiffusionScorerFactory(name string, rawParameters json.RawMessage, handle fwkplugin.Handle) (fwkplugin.Plugin, error) {
	parameters := diffusionScorerParameters{QueueThreshold: DiffusionQueueThresholdDefault}
	if rawParameters != nil {
		if err := json.Unmarshal(rawParameters, &parameters); err != nil {
			return nil, fmt.Errorf("failed to parse the parameters of the '%s' scorer - %w", DiffusionScorerType, err)
		}
	}
	return NewDiffusionScorer(handle.Context(), parameters.QueueThreshold).WithName(name), nil
}

// DiffusionScorer scores endpoints for image generation workloads (e.g. Stable Diffusion, FLUX).
// Primary signal: queue depth (denoising is GPU-intensive and long-running).
// Secondary signal: GPU memory (diffusion models hold large latent tensors).
type DiffusionScorer struct {
	typedName      fwkplugin.TypedName
	queueThreshold float64
}

// NewDiffusionScorer creates a new DiffusionScorer instance.
func NewDiffusionScorer(ctx context.Context, queueThreshold int) *DiffusionScorer {
	if queueThreshold <= 0 {
		queueThreshold = DiffusionQueueThresholdDefault
		log.FromContext(ctx).V(logutil.DEFAULT).Info(fmt.Sprintf(
			"Diffusion queueThreshold %d should be positive, using default %d",
			queueThreshold, DiffusionQueueThresholdDefault))
	}
	return &DiffusionScorer{
		typedName:      fwkplugin.TypedName{Type: DiffusionScorerType},
		queueThreshold: float64(queueThreshold),
	}
}

// WithName sets the name of the plugin.
func (s *DiffusionScorer) WithName(name string) *DiffusionScorer {
	s.typedName.Name = name
	return s
}

// TypedName returns the typed name of the plugin.
func (s *DiffusionScorer) TypedName() fwkplugin.TypedName {
	return s.typedName
}

// Category returns the preference the scorer applies when scoring candidate endpoints.
func (s *DiffusionScorer) Category() fwksched.ScorerCategory {
	return fwksched.Distribution
}

// Consumes returns the data consumed by this scorer.
func (s *DiffusionScorer) Consumes() map[string]any {
	return map[string]any{
		metrics.WaitingQueueSizeKey:    int(0),
		metrics.KVCacheUsagePercentKey: float64(0),
	}
}

// Score scores endpoints for diffusion model workloads. Diffusion models run
// iterative denoising (20-50 steps per image) making them GPU-bound with long
// execution times. Queue depth is the dominant signal.
func (s *DiffusionScorer) Score(_ context.Context, _ *fwksched.CycleState, _ *fwksched.InferenceRequest, endpoints []fwksched.Endpoint) map[fwksched.Endpoint]float64 {
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

		// 80% queue depth (dominant for diffusion), 20% GPU memory
		scores[ep] = 0.8*queueScore + 0.2*gpuMemScore
	}
	return scores
}
