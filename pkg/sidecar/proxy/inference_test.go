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

package proxy

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInferenceHandler_MissingEndpoint(t *testing.T) {
	body := `{"body": {"prompt": "hello"}}`

	var ir inferenceRequest
	err := json.Unmarshal([]byte(body), &ir)
	require.NoError(t, err)
	assert.Empty(t, ir.Endpoint, "endpoint should be empty")
}

func TestInferenceHandler_ParsesRoutingHints(t *testing.T) {
	body := `{
		"endpoint": "/v1/audio/speech",
		"model": "tts-1",
		"routing_hints": {
			"prefer_disaggregated": true,
			"preferred_arch": "omni-llm",
			"session_token": "abc123"
		},
		"body": {"input": "Hello world", "voice": "alloy"}
	}`

	var ir inferenceRequest
	err := json.Unmarshal([]byte(body), &ir)
	require.NoError(t, err)

	assert.Equal(t, "/v1/audio/speech", ir.Endpoint)
	assert.Equal(t, "tts-1", ir.Model)
	require.NotNil(t, ir.RoutingHints)
	assert.True(t, ir.RoutingHints.PreferDisaggregated)
	assert.Equal(t, "omni-llm", ir.RoutingHints.PreferredArch)
	assert.Equal(t, "abc123", ir.RoutingHints.SessionToken)
	assert.NotNil(t, ir.Body)
}

func TestInferenceHandler_MinimalValidRequest(t *testing.T) {
	body := `{
		"endpoint": "/v1/chat/completions",
		"body": {"model": "gpt-4", "messages": [{"role": "user", "content": "hi"}]}
	}`

	var ir inferenceRequest
	err := json.Unmarshal([]byte(body), &ir)
	require.NoError(t, err)

	assert.Equal(t, "/v1/chat/completions", ir.Endpoint)
	assert.Nil(t, ir.RoutingHints)
	assert.NotNil(t, ir.Body)
}

func TestInferenceHandler_InvalidJSON(t *testing.T) {
	body := `{not valid json`

	var ir inferenceRequest
	err := json.Unmarshal([]byte(body), &ir)
	assert.Error(t, err)
}
