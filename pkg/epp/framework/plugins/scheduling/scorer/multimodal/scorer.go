/*
Copyright 2026 The llm-d Authors.

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

package multimodal

import (
	"context"
	"encoding/json"

	"github.com/llm-d/llm-d-router/pkg/common"
	fwkplugin "github.com/llm-d/llm-d-router/pkg/epp/framework/interface/plugin"
	fwksched "github.com/llm-d/llm-d-router/pkg/epp/framework/interface/scheduling"
)

const ScorerName = "multimodal-load-scorer"

type MultimodalLoadScorer struct {
	name string
}

func MultimodalLoadScorerFactory(name string, _ *json.Decoder, _ fwkplugin.Handle) (fwkplugin.Plugin, error) {
	return &MultimodalLoadScorer{name: name}, nil
}

func (s *MultimodalLoadScorer) Name() string {
	return ScorerName
}

func (s *MultimodalLoadScorer) TypedName() fwkplugin.TypedName {
	return fwkplugin.TypedName{
		Type: "scorer",
		Name: ScorerName,
	}
}

func (s *MultimodalLoadScorer) Score(ctx context.Context, req *fwksched.InferenceRequest, endpoints []fwksched.Endpoint) (map[fwksched.Endpoint]float64, error) {
	scores := make(map[fwksched.Endpoint]float64)

	var path string
	if req != nil && req.Headers != nil {
		path = req.Headers[common.EnvoyPathHeader]
	}

	allowedArchs, isMultimodal := common.PathToModelArch[path]
	if !isMultimodal {
		for _, endpoint := range endpoints {
			scores[endpoint] = 0.0
		}
		return scores, nil
	}

	for _, endpoint := range endpoints {
		var podArch string
		if meta := endpoint.GetMetadata(); meta != nil && meta.Labels != nil {
			podArch = meta.Labels[common.ModelArchLabel]
		}

		isMatch := false
		for _, arch := range allowedArchs {
			if podArch == arch {
				isMatch = true
				break
			}
		}

		if isMatch {
			scores[endpoint] = 100.0
		} else {
			scores[endpoint] = 0.0
		}
	}

	return scores, nil
}

func init() {
	fwkplugin.Registry[ScorerName] = MultimodalLoadScorerFactory
}
