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
	"testing"

	"github.com/stretchr/testify/assert"

	fwkdl "github.com/llm-d/llm-d-router/pkg/epp/framework/interface/datalayer"
	fwksched "github.com/llm-d/llm-d-router/pkg/epp/framework/interface/scheduling"
)

func TestTTSScorer_EmptyQueueGetsHighScore(t *testing.T) {
	s := NewTTSScorer(context.Background(), TTSQueueThresholdDefault)
	endpoints := []fwksched.Endpoint{
		fwksched.NewEndpoint(&fwkdl.EndpointMetadata{}, &fwkdl.Metrics{
			WaitingQueueSize:    0,
			KVCacheUsagePercent: 0.2,
		}, nil),
	}

	scores := s.Score(context.Background(), nil, nil, endpoints)
	score := scores[endpoints[0]]
	// 0.7 * 1.0 + 0.3 * 0.8 = 0.94
	assert.InDelta(t, 0.94, score, 0.01, "idle endpoint should score ~0.94")
}

func TestTTSScorer_FullQueueGetsLowScore(t *testing.T) {
	s := NewTTSScorer(context.Background(), TTSQueueThresholdDefault)
	endpoints := []fwksched.Endpoint{
		fwksched.NewEndpoint(&fwkdl.EndpointMetadata{}, &fwkdl.Metrics{
			WaitingQueueSize:    100,
			KVCacheUsagePercent: 0.9,
		}, nil),
	}

	scores := s.Score(context.Background(), nil, nil, endpoints)
	score := scores[endpoints[0]]
	// 0.7 * 0.0 + 0.3 * 0.1 = 0.03
	assert.InDelta(t, 0.03, score, 0.01, "saturated endpoint should score ~0.03")
}

func TestTTSScorer_PrefersLeastLoaded(t *testing.T) {
	s := NewTTSScorer(context.Background(), TTSQueueThresholdDefault)
	endpoints := []fwksched.Endpoint{
		fwksched.NewEndpoint(&fwkdl.EndpointMetadata{}, &fwkdl.Metrics{
			WaitingQueueSize:    10,
			KVCacheUsagePercent: 0.3,
		}, nil),
		fwksched.NewEndpoint(&fwkdl.EndpointMetadata{}, &fwkdl.Metrics{
			WaitingQueueSize:    50,
			KVCacheUsagePercent: 0.3,
		}, nil),
	}

	scores := s.Score(context.Background(), nil, nil, endpoints)
	assert.Greater(t, scores[endpoints[0]], scores[endpoints[1]],
		"light endpoint should score higher than heavy endpoint")
}

func TestTTSScorer_QueueAtThresholdGetsZeroQueueScore(t *testing.T) {
	s := NewTTSScorer(context.Background(), 64)
	endpoints := []fwksched.Endpoint{
		fwksched.NewEndpoint(&fwkdl.EndpointMetadata{}, &fwkdl.Metrics{
			WaitingQueueSize:    64,
			KVCacheUsagePercent: 0.0,
		}, nil),
	}

	scores := s.Score(context.Background(), nil, nil, endpoints)
	score := scores[endpoints[0]]
	// 0.7 * 0.0 + 0.3 * 1.0 = 0.3
	assert.InDelta(t, 0.3, score, 0.01, "endpoint at queue threshold gets only GPU memory score")
}
