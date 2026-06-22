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

package modality_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/llm-d/llm-d-router/pkg/common"
	fwkdl "github.com/llm-d/llm-d-router/pkg/epp/framework/interface/datalayer"
	fwksched "github.com/llm-d/llm-d-router/pkg/epp/framework/interface/scheduling"
	"github.com/llm-d/llm-d-router/pkg/epp/framework/plugins/scheduling/filter/modality"
)

func createModalityEndpoint(name string, labels map[string]string) fwksched.Endpoint {
	return fwksched.NewEndpoint(&fwkdl.EndpointMetadata{Labels: labels}, &fwkdl.Metrics{}, nil)
}

func TestModalityFilter_TextPathPassesAll(t *testing.T) {
	f := modality.NewModalityFilter()
	endpoints := []fwksched.Endpoint{
		createModalityEndpoint("text-pod", map[string]string{common.ModelArchLabel: common.ModelArchAutoRegressLLM}),
		createModalityEndpoint("tts-pod", map[string]string{common.ModelArchLabel: common.ModelArchOmniLLM}),
	}
	request := &fwksched.InferenceRequest{
		Headers: map[string]string{common.EnvoyPathHeader: "/v1/chat/completions"},
	}

	result := f.Filter(context.Background(), nil, request, endpoints)
	assert.Len(t, result, 2, "text path should pass all endpoints through unfiltered")
}

func TestModalityFilter_AudioSpeechFiltersCorrectly(t *testing.T) {
	f := modality.NewModalityFilter()
	endpoints := []fwksched.Endpoint{
		createModalityEndpoint("omni-pod", map[string]string{common.ModelArchLabel: common.ModelArchOmniLLM}),
		createModalityEndpoint("tts-pod", map[string]string{common.ModelArchLabel: common.ModelArchAutoRegressTTS}),
		createModalityEndpoint("text-pod", map[string]string{common.ModelArchLabel: common.ModelArchAutoRegressLLM}),
		createModalityEndpoint("diffusion-pod", map[string]string{common.ModelArchLabel: common.ModelArchDiffusion}),
	}
	request := &fwksched.InferenceRequest{
		Headers: map[string]string{common.EnvoyPathHeader: common.AudioSpeechPath},
	}

	result := f.Filter(context.Background(), nil, request, endpoints)
	assert.Len(t, result, 2, "audio/speech should match omni-llm and autoregressive-tts endpoints")
}

func TestModalityFilter_QueryParamsStripped(t *testing.T) {
	f := modality.NewModalityFilter()
	endpoints := []fwksched.Endpoint{
		createModalityEndpoint("omni-pod", map[string]string{common.ModelArchLabel: common.ModelArchOmniLLM}),
		createModalityEndpoint("text-pod", map[string]string{common.ModelArchLabel: common.ModelArchAutoRegressLLM}),
	}
	request := &fwksched.InferenceRequest{
		Headers: map[string]string{common.EnvoyPathHeader: "/v1/audio/speech?model=tts-1"},
	}

	result := f.Filter(context.Background(), nil, request, endpoints)
	assert.Len(t, result, 1, "should filter correctly even with query params")
}

func TestModalityFilter_EmptyPath(t *testing.T) {
	f := modality.NewModalityFilter()
	endpoints := []fwksched.Endpoint{
		createModalityEndpoint("pod-1", map[string]string{common.ModelArchLabel: common.ModelArchOmniLLM}),
	}
	request := &fwksched.InferenceRequest{
		Headers: map[string]string{},
	}

	result := f.Filter(context.Background(), nil, request, endpoints)
	assert.Len(t, result, 1, "empty path should pass all endpoints (unknown path = no filtering)")
}

func TestModalityFilter_ImagesGenerationsFiltersDiffusion(t *testing.T) {
	f := modality.NewModalityFilter()
	endpoints := []fwksched.Endpoint{
		createModalityEndpoint("diffusion-pod", map[string]string{common.ModelArchLabel: common.ModelArchDiffusion}),
		createModalityEndpoint("text-pod", map[string]string{common.ModelArchLabel: common.ModelArchAutoRegressLLM}),
		createModalityEndpoint("tts-pod", map[string]string{common.ModelArchLabel: common.ModelArchAutoRegressTTS}),
	}
	request := &fwksched.InferenceRequest{
		Headers: map[string]string{common.EnvoyPathHeader: common.ImagesGenerationsPath},
	}

	result := f.Filter(context.Background(), nil, request, endpoints)
	assert.Len(t, result, 1, "images/generations should match only diffusion endpoints")
}
