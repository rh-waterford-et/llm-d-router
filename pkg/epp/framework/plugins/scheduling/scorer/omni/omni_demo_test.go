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
	"fmt"
	"strings"
	"testing"

	"github.com/llm-d/llm-d-router/pkg/common"
	fwkdl "github.com/llm-d/llm-d-router/pkg/epp/framework/interface/datalayer"
	fwksched "github.com/llm-d/llm-d-router/pkg/epp/framework/interface/scheduling"
)

type demoEndpoint struct {
	name      string
	archLabel string
	model     string
	waiting   bool
	queue     int
}

func makeDemoEndpoint(d demoEndpoint) fwksched.Endpoint {
	labels := map[string]string{}
	if d.archLabel != "" {
		labels[common.ModelArchLabel] = d.archLabel
	}
	active := map[string]int{}
	waitingModels := map[string]int{}
	if d.model != "" {
		if d.waiting {
			waitingModels[d.model] = 1
			active["placeholder-model"] = 1
		} else {
			active[d.model] = 1
		}
	}
	return fwksched.NewEndpoint(
		&fwkdl.EndpointMetadata{Labels: labels},
		&fwkdl.Metrics{
			ActiveModels:     active,
			WaitingModels:    waitingModels,
			WaitingQueueSize: d.queue,
		},
		nil,
	)
}

type scenario struct {
	title       string
	path        string
	targetModel string
	endpoints   []demoEndpoint
}

func printScenario(t *testing.T, s *OmniLLMScorer, sc scenario) {
	t.Helper()

	eps := make([]fwksched.Endpoint, len(sc.endpoints))
	for i, d := range sc.endpoints {
		eps[i] = makeDemoEndpoint(d)
	}

	req := &fwksched.InferenceRequest{
		TargetModel: sc.targetModel,
		Headers:     map[string]string{common.EnvoyPathHeader: sc.path},
	}
	scores := s.Score(context.Background(), req, eps)

	var bestIdx int
	var bestScore float64
	for i, ep := range eps {
		if scores[ep] > bestScore {
			bestScore = scores[ep]
			bestIdx = i
		}
	}

	sep := strings.Repeat("─", 95)
	t.Logf("")
	t.Logf("┌%s┐", sep)
	t.Logf("│ %-93s │", sc.title)
	t.Logf("│ %-93s │", fmt.Sprintf("Path: %s    Model: %s", sc.path, sc.targetModel))
	t.Logf("├──────────────┬────────────────────────┬──────────────────────┬───────┬────────┬───────┤")
	t.Logf("│ %-12s │ %-22s │ %-20s │ %5s │ %6s │ %5s │", "Endpoint", "model-arch", "Model State", "Queue", "Base", "Final")
	t.Logf("├──────────────┼────────────────────────┼──────────────────────┼───────┼────────┼───────┤")

	for i, d := range sc.endpoints {
		arch := d.archLabel
		if arch == "" {
			arch = "(none)"
		}
		modelState := d.model
		if d.waiting {
			modelState += " (loading)"
		} else if d.model != "" {
			modelState += " (active)"
		} else {
			modelState = "(none)"
		}
		score := scores[eps[i]]

		base := s.baseScore(eps[i], sc.targetModel, archSetForPath(requestPath(req)), archSetForPath(requestPath(req)) != nil)

		marker := "  "
		if i == bestIdx && bestScore > 0 {
			marker = "◀─"
		}

		t.Logf("│ %-12s │ %-22s │ %-20s │ %5d │ %6.2f │ %5.2f │ %s",
			d.name, arch, modelState, d.queue, base, score, marker)
	}
	t.Logf("└──────────────┴────────────────────────┴──────────────────────┴───────┴────────┴───────┘")
	if bestScore > 0 {
		t.Logf("  Winner: %s (score %.2f)", sc.endpoints[bestIdx].name, bestScore)
	} else {
		t.Logf("  ⚠ All endpoints scored 0.0 — misconfiguration likely")
	}
}

func TestDemo_ScoringScenarios(t *testing.T) {
	s := NewOmniLLMScorer(context.Background(), QueueThresholdDefault)

	scenarios := []scenario{
		{
			title:       "Scenario 1: Mixed pool — audio/speech request",
			path:        common.AudioSpeechPath,
			targetModel: "qwen-omni",
			endpoints: []demoEndpoint{
				{name: "omni-pod-A", archLabel: common.ModelArchOmniLLM, model: "qwen-omni", queue: 5},
				{name: "omni-pod-B", archLabel: common.ModelArchOmniLLM, model: "qwen-omni", queue: 90},
				{name: "omni-pod-C", archLabel: common.ModelArchOmniLLM, model: "qwen-omni", waiting: true, queue: 10},
				{name: "omni-pod-D", archLabel: common.ModelArchOmniLLM, model: "qwen-omni-7b", queue: 0},
				{name: "diff-pod-E", archLabel: common.ModelArchDiffusion, model: "sd-xl", queue: 0},
				{name: "bare-pod-F", archLabel: "", model: "qwen-omni", queue: 0},
			},
		},
		{
			title:       "Scenario 2: Text completions — arch check skipped",
			path:        "/v1/chat/completions",
			targetModel: "qwen-omni",
			endpoints: []demoEndpoint{
				{name: "omni-pod-A", archLabel: common.ModelArchOmniLLM, model: "qwen-omni", queue: 0},
				{name: "llm-pod-B", archLabel: common.ModelArchAutoRegressLLM, model: "llama-70b", queue: 0},
				{name: "llm-pod-C", archLabel: common.ModelArchAutoRegressLLM, model: "qwen-omni", queue: 30},
			},
		},
		{
			title:       "Scenario 3: All omni pods — queue depth tiebreaker",
			path:        common.AudioSpeechPath,
			targetModel: "qwen-omni",
			endpoints: []demoEndpoint{
				{name: "omni-pod-A", archLabel: common.ModelArchOmniLLM, model: "qwen-omni", queue: 0},
				{name: "omni-pod-B", archLabel: common.ModelArchOmniLLM, model: "qwen-omni", queue: 64},
				{name: "omni-pod-C", archLabel: common.ModelArchOmniLLM, model: "qwen-omni", queue: 128},
				{name: "omni-pod-D", archLabel: common.ModelArchOmniLLM, model: "qwen-omni", queue: 256},
			},
		},
		{
			title:       "Scenario 4: Three-tier ordering under max load",
			path:        common.AudioSpeechPath,
			targetModel: "qwen-omni",
			endpoints: []demoEndpoint{
				{name: "active-128q", archLabel: common.ModelArchOmniLLM, model: "qwen-omni", queue: 128},
				{name: "loading-128q", archLabel: common.ModelArchOmniLLM, model: "qwen-omni", waiting: true, queue: 128},
				{name: "other-128q", archLabel: common.ModelArchOmniLLM, model: "qwen-omni-7b", queue: 128},
				{name: "active-idle", archLabel: common.ModelArchOmniLLM, model: "qwen-omni", queue: 0},
			},
		},
		{
			title:       "Scenario 5: Image generation — diffusion pods excluded",
			path:        common.ImagesGenerationsPath,
			targetModel: "sd-xl",
			endpoints: []demoEndpoint{
				{name: "diff-pod-A", archLabel: common.ModelArchDiffusion, model: "sd-xl", queue: 0},
				{name: "diff-pod-B", archLabel: common.ModelArchDiffusion, model: "sd-xl-turbo", queue: 0},
				{name: "omni-pod-C", archLabel: common.ModelArchOmniLLM, model: "qwen-omni", queue: 0},
			},
		},
	}

	t.Logf("")
	t.Logf("═══════════════════════════════════════════════════════════════════════════════════════════════════")
	t.Logf("  omni-llm-scorer — Scoring Demo")
	t.Logf("  Three tiers: Active=1.0  Loading=0.75  Arch-only=0.5  Incompatible=0.0")
	t.Logf("  Queue penalty: min(queue/%d, 1.0) × %.1f", QueueThresholdDefault, queuePenaltyWeight)
	t.Logf("═══════════════════════════════════════════════════════════════════════════════════════════════════")

	for _, sc := range scenarios {
		printScenario(t, s, sc)
	}
}
