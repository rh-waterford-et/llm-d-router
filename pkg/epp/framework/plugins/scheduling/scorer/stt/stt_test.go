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
	"testing"

	"github.com/stretchr/testify/assert"

	fwkdl "github.com/llm-d/llm-d-router/pkg/epp/framework/interface/datalayer"
	fwksched "github.com/llm-d/llm-d-router/pkg/epp/framework/interface/scheduling"
)

func TestSTTScorer_IdlePodGetsHighScore(t *testing.T) {
	s := NewSTTScorer(context.Background(), STTQueueThresholdDefault)
	endpoints := []fwksched.Endpoint{
		fwksched.NewEndpoint(&fwkdl.EndpointMetadata{}, &fwkdl.Metrics{
			WaitingQueueSize:    0,
			RunningRequestsSize: 0,
		}, nil),
	}

	scores := s.Score(context.Background(), nil, nil, endpoints)
	score := scores[endpoints[0]]
	// 0.6 * 1.0 + 0.4 * 1.0 = 1.0
	assert.InDelta(t, 1.0, score, 0.01)
}

func TestSTTScorer_PrefersLessActiveRequests(t *testing.T) {
	s := NewSTTScorer(context.Background(), STTQueueThresholdDefault)
	endpoints := []fwksched.Endpoint{
		fwksched.NewEndpoint(&fwkdl.EndpointMetadata{}, &fwkdl.Metrics{
			WaitingQueueSize:    10,
			RunningRequestsSize: 5,
		}, nil),
		fwksched.NewEndpoint(&fwkdl.EndpointMetadata{}, &fwkdl.Metrics{
			WaitingQueueSize:    10,
			RunningRequestsSize: 50,
		}, nil),
	}

	scores := s.Score(context.Background(), nil, nil, endpoints)
	assert.Greater(t, scores[endpoints[0]], scores[endpoints[1]],
		"endpoint with fewer active requests should score higher (more encoder batch headroom)")
}

func TestSTTScorer_SaturatedPodGetsLowScore(t *testing.T) {
	s := NewSTTScorer(context.Background(), STTQueueThresholdDefault)
	endpoints := []fwksched.Endpoint{
		fwksched.NewEndpoint(&fwkdl.EndpointMetadata{}, &fwkdl.Metrics{
			WaitingQueueSize:    100,
			RunningRequestsSize: 100,
		}, nil),
	}

	scores := s.Score(context.Background(), nil, nil, endpoints)
	score := scores[endpoints[0]]
	assert.InDelta(t, 0.0, score, 0.01, "fully saturated endpoint should score ~0")
}
