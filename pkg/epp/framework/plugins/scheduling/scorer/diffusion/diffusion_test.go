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
	"testing"

	"github.com/stretchr/testify/assert"

	fwkdl "github.com/llm-d/llm-d-router/pkg/epp/framework/interface/datalayer"
	fwksched "github.com/llm-d/llm-d-router/pkg/epp/framework/interface/scheduling"
)

func TestDiffusionScorer_IdlePodGetsHighScore(t *testing.T) {
	s := NewDiffusionScorer(context.Background(), DiffusionQueueThresholdDefault)
	endpoints := []fwksched.Endpoint{
		fwksched.NewEndpoint(&fwkdl.EndpointMetadata{}, &fwkdl.Metrics{
			WaitingQueueSize:    0,
			KVCacheUsagePercent: 0.1,
		}, nil),
	}

	scores := s.Score(context.Background(), nil, endpoints)
	score := scores[endpoints[0]]
	// 0.8 * 1.0 + 0.2 * 0.9 = 0.98
	assert.InDelta(t, 0.98, score, 0.01)
}

func TestDiffusionScorer_QueueHeavilyWeighted(t *testing.T) {
	s := NewDiffusionScorer(context.Background(), DiffusionQueueThresholdDefault)
	endpoints := []fwksched.Endpoint{
		fwksched.NewEndpoint(&fwkdl.EndpointMetadata{}, &fwkdl.Metrics{
			WaitingQueueSize:    16,
			KVCacheUsagePercent: 0.0,
		}, nil),
		fwksched.NewEndpoint(&fwkdl.EndpointMetadata{}, &fwkdl.Metrics{
			WaitingQueueSize:    0,
			KVCacheUsagePercent: 0.9,
		}, nil),
	}

	scores := s.Score(context.Background(), nil, endpoints)
	// queued: 0.8 * 0.5 + 0.2 * 1.0 = 0.6
	// memory: 0.8 * 1.0 + 0.2 * 0.1 = 0.82
	assert.Greater(t, scores[endpoints[1]], scores[endpoints[0]],
		"queue depth should dominate over GPU memory for diffusion")
}

func TestDiffusionScorer_LowThreshold(t *testing.T) {
	s := NewDiffusionScorer(context.Background(), 32)
	endpoints := []fwksched.Endpoint{
		fwksched.NewEndpoint(&fwkdl.EndpointMetadata{}, &fwkdl.Metrics{
			WaitingQueueSize:    32,
			KVCacheUsagePercent: 0.5,
		}, nil),
	}

	scores := s.Score(context.Background(), nil, endpoints)
	score := scores[endpoints[0]]
	// 0.8 * 0.0 + 0.2 * 0.5 = 0.1
	assert.InDelta(t, 0.1, score, 0.01)
}
