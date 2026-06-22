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
	"io"
	"net/http"
	"strings"

	"github.com/llm-d/llm-d-router/pkg/common/routing"
)

// inferenceRequest represents the custom /v1/inference request body with
// explicit routing hints that the client can use to direct the EPP.
type inferenceRequest struct {
	// Endpoint is the underlying endpoint to route to (e.g. "/v1/audio/speech")
	Endpoint string `json:"endpoint"`

	// Model is the target model name
	Model string `json:"model,omitempty"`

	// RoutingHints provides explicit routing preferences
	RoutingHints *routingHints `json:"routing_hints,omitempty"`

	// Body is the raw request body to forward to the target endpoint
	Body json.RawMessage `json:"body"`
}

// routingHints allows clients to express routing preferences.
type routingHints struct {
	// PreferDisaggregated hints that the client prefers disaggregated execution
	PreferDisaggregated bool `json:"prefer_disaggregated,omitempty"`

	// PreferredArch hints at the preferred model architecture
	PreferredArch string `json:"preferred_arch,omitempty"`

	// SessionToken for session affinity
	SessionToken string `json:"session_token,omitempty"`
}

// inferenceHandler handles /v1/inference requests.
// This is a custom JSON wrapper endpoint that accepts explicit routing hints
// from clients. It extracts the target endpoint from the body and proxies
// the inner request to the appropriate handler, passing routing hints as
// headers for the EPP to consume.
func (s *Server) inferenceHandler(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.logger.Error(err, "inference: failed to read request body")
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var req inferenceRequest
	if err := json.Unmarshal(body, &req); err != nil {
		s.logger.Error(err, "inference: failed to parse request body")
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Endpoint == "" {
		http.Error(w, "missing required field: endpoint", http.StatusBadRequest)
		return
	}

	if req.Body == nil {
		http.Error(w, "missing required field: body", http.StatusBadRequest)
		return
	}

	if req.RoutingHints != nil {
		if req.RoutingHints.PreferDisaggregated {
			r.Header.Set("x-routing-prefer-disaggregated", "true")
		}
		if req.RoutingHints.PreferredArch != "" {
			r.Header.Set("x-routing-preferred-arch", req.RoutingHints.PreferredArch)
		}
		if req.RoutingHints.SessionToken != "" {
			r.Header.Set("x-session-token", req.RoutingHints.SessionToken)
		}
	}

	r.Body = io.NopCloser(strings.NewReader(string(req.Body)))
	r.ContentLength = int64(len(req.Body))
	r.URL.Path = req.Endpoint

	prefillHostPort := r.Header.Get(routing.PrefillEndpointHeader)

	if prefillHostPort == "" {
		s.logger.V(4).Info("inference: monolithic execution", "targetEndpoint", req.Endpoint)
		s.decoderProxy.ServeHTTP(w, r)
		return
	}

	if !s.allowlistValidator.IsAllowed(prefillHostPort) {
		s.logger.Error(nil, "SSRF protection: prefill target not in allowlist",
			"target", prefillHostPort,
			"clientIP", r.RemoteAddr,
			"requestPath", r.URL.Path)
		http.Error(w, "Forbidden: prefill target not allowed by SSRF protection", http.StatusForbidden)
		return
	}

	s.logger.V(4).Info("inference: disaggregated execution",
		"targetEndpoint", req.Endpoint, "prefillTarget", prefillHostPort)
	s.runPDConnectorProtocol(w, r, prefillHostPort, APITypeChatCompletions)
}
