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

package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	v1 "sigs.k8s.io/gateway-api-inference-extension/api/v1"

	"github.com/llm-d/llm-d-router/pkg/epp/framework/common/request"
	fwkplugin "github.com/llm-d/llm-d-router/pkg/epp/framework/interface/plugin"
	fwkrh "github.com/llm-d/llm-d-router/pkg/epp/framework/interface/requesthandling"
	"github.com/llm-d/llm-d-router/pkg/epp/framework/plugins/requesthandling/parsers"
)

const (
	OpenAIParserType = "openai-parser"

	conversationsAPI       = "conversations"
	responsesAPI           = "responses"
	chatCompletionsAPI     = "chat/completions"
	completionsAPI         = "completions"
	embeddingsAPI          = "embeddings"
	imagesGenerationsAPI   = "images/generations"
	audioTranscriptionsAPI = "audio/transcriptions"
	inferenceAPI           = "inference"
	audioSpeechAPI         = "audio/speech"

	streamingRespPrefix = "data: "
	streamingEndMsg     = "data: [DONE]"

	objectTypeResponse            = "response"
	objectTypeConversation        = "conversation"
	objectTypeChatCompletion      = "chat.completion"
	objectTypeChatCompletionChunk = "chat.completion.chunk"
	objectTypeTemplateCompletion  = "text_completion"

	contentType     = "content-type"
	eventStreamType = "text/event-stream"
)

// compile-time type validation
var (
	_ fwkrh.Parser            = &OpenAIParser{}
	_ fwkrh.ModelNameRewriter = &OpenAIParser{}
)

type OpenAIParser struct {
	typedName fwkplugin.TypedName
}

func NewOpenAIParser() *OpenAIParser {
	return &OpenAIParser{
		typedName: fwkplugin.TypedName{
			Type: OpenAIParserType,
			Name: OpenAIParserType,
		},
	}
}

func (p *OpenAIParser) TypedName() fwkplugin.TypedName {
	return p.typedName
}

func (p *OpenAIParser) SupportedAppProtocols() []v1.AppProtocol {
	return []v1.AppProtocol{v1.AppProtocolH2C, v1.AppProtocolHTTP}
}

func (p *OpenAIParser) Claims() fwkrh.Claims {
	return fwkrh.Claims{
		Paths: []string{
			chatCompletionsAPI,
			completionsAPI,
			embeddingsAPI,
			responsesAPI,
			conversationsAPI,
			chatCompletionsAPI + "/render",
			completionsAPI + "/render",
			imagesGenerationsAPI,
			audioSpeechAPI,
			audioTranscriptionsAPI,
			inferenceAPI,
		},
		Protocols: []v1.AppProtocol{v1.AppProtocolH2C, v1.AppProtocolHTTP},
	}
}

func OpenAIParserPluginFactory(name string, _ *json.Decoder, _ fwkplugin.Handle) (fwkplugin.Plugin, error) {
	return NewOpenAIParser().WithName(name), nil
}

func (p *OpenAIParser) WithName(name string) *OpenAIParser {
	p.typedName.Name = name
	return p
}

func (p *OpenAIParser) ParseRequest(ctx context.Context, body []byte, headers map[string]string) (*fwkrh.ParseResult, error) {
	bodyMap := make(map[string]any)
	if err := json.Unmarshal(body, &bodyMap); err != nil {
		return nil, fmt.Errorf("error unmarshaling request bodyMap: %w", err)
	}

	path := getRequestPath(headers)
	apiType := determineAPITypeFromPath(path)

	extractedBody, err := extractRequestBody(apiType, body)
	if err != nil {
		return nil, err
	}
	extractedBody.Payload = fwkrh.PayloadMap(bodyMap)
	if model, ok := bodyMap["model"].(string); ok {
		extractedBody.Model = model
	}
	extractedBody.MaxOutputTokens = maxOutputTokensForAPI(apiType, bodyMap)
	if stream, ok := bodyMap["stream"].(bool); ok && stream {
		extractedBody.Stream = true
	}

	return &fwkrh.ParseResult{Body: extractedBody}, nil
}

// RewriteModelName writes the resolved model into the request payload map.
func (p *OpenAIParser) RewriteModelName(payload fwkrh.MarshalablePayload, model string) (fwkrh.MarshalablePayload, error) {
	m, ok := payload.(fwkrh.PayloadMap)
	if !ok {
		return payload, nil
	}
	m["model"] = model
	return m, nil
}

// maxOutputTokensForAPI normalizes the per-API output-token cap field into a
// single value, applying each API's field name and precedence. Endpoints with no
// output-token concept (conversations, embeddings) return nil.
func maxOutputTokensForAPI(apiType string, bodyMap map[string]any) *int64 {
	switch apiType {
	case chatCompletionsAPI:
		return fwkrh.MaxOutputTokensFromPayload(bodyMap, "max_completion_tokens", "max_tokens")
	case completionsAPI:
		return fwkrh.MaxOutputTokensFromPayload(bodyMap, "max_tokens")
	case responsesAPI:
		return fwkrh.MaxOutputTokensFromPayload(bodyMap, "max_output_tokens")
	default:
		return nil
	}
}

func (p *OpenAIParser) ParseResponse(ctx context.Context, body []byte, headers map[string]string, _ bool) (*fwkrh.ParsedResponse, error) {
	if len(body) == 0 {
		return nil, nil //nolint:nilnil
	}

	isStream := false
	for k, v := range headers {
		if strings.ToLower(k) == contentType && strings.Contains(strings.ToLower(v), eventStreamType) {
			isStream = true
			break
		}
	}
	if isStream {
		return p.parseStreamResponse(body)
	}

	usage, err := extractUsage(body)
	if err != nil {
		return nil, err
	}
	return &fwkrh.ParsedResponse{Usage: usage}, nil
}

func (p *OpenAIParser) parseStreamResponse(chunk []byte) (*fwkrh.ParsedResponse, error) {
	usage := extractUsageStreaming(string(chunk))
	return &fwkrh.ParsedResponse{Usage: usage}, nil
}

func getRequestPath(headers map[string]string) string {
	if path := headers[parsers.MethodPathKey]; path != "" {
		return path
	}
	if path := headers["x-original-path"]; path != "" {
		return path
	}
	if path := headers["x-forwarded-path"]; path != "" {
		return path
	}
	return "/v1/completions"
}

func determineAPITypeFromPath(path string) string {
	path = strings.TrimSuffix(path, "/")
	if strings.HasSuffix(path, "/conversations") {
		return conversationsAPI
	}
	if strings.HasSuffix(path, "/responses") {
		return responsesAPI
	}
	if strings.HasSuffix(path, "/chat/completions") || strings.HasSuffix(path, "/chat/completions/render") {
		return chatCompletionsAPI
	}
	if strings.HasSuffix(path, "/completions") || strings.HasSuffix(path, "/completions/render") {
		return completionsAPI
	}
	if strings.HasSuffix(path, "/embeddings") {
		return embeddingsAPI
	}
	if request.MatchPathSuffix(path, "/audio/speech") {
		return audioSpeechAPI
	}
	if request.MatchPathSuffix(path, "/audio/transcriptions") {
		return audioTranscriptionsAPI
	}
	if request.MatchPathSuffix(path, "/images/generations") {
		return imagesGenerationsAPI
	}
	if request.MatchPathSuffix(path, "/inference") {
		return inferenceAPI
	}
	// Default to completions API for backward compatibility with existing clients and integration tests
	return completionsAPI
}

func extractRequestBody(apiType string, rawBody []byte) (*fwkrh.InferenceRequestBody, error) {
	switch apiType {
	case conversationsAPI:
		var conversations fwkrh.ConversationsRequest
		if err := json.Unmarshal(rawBody, &conversations); err == nil && len(conversations.Items) > 0 {
			return &fwkrh.InferenceRequestBody{Conversations: &conversations}, nil
		}
		return nil, errors.New("invalid conversations request: must have items field")

	case responsesAPI:
		var responses fwkrh.ResponsesRequest
		if err := json.Unmarshal(rawBody, &responses); err == nil && responses.Input != nil {
			return &fwkrh.InferenceRequestBody{Responses: &responses}, nil
		}
		return nil, errors.New("invalid responses request: must have input field")

	case chatCompletionsAPI:
		var chatCompletions fwkrh.ChatCompletionsRequest
		if err := json.Unmarshal(rawBody, &chatCompletions); err == nil {
			if err = validateChatCompletionsMessages(chatCompletions.Messages); err == nil {
				return &fwkrh.InferenceRequestBody{ChatCompletions: &chatCompletions}, nil
			}
		}
		return nil, errors.New("invalid chat completions request: must have valid messages field")

	case completionsAPI:
		var completions fwkrh.CompletionsRequest
		if err := json.Unmarshal(rawBody, &completions); err == nil && !completions.Prompt.IsEmpty() {
			return &fwkrh.InferenceRequestBody{Completions: &completions}, nil
		}
		return nil, errors.New("invalid completions request: must have prompt field")

	case embeddingsAPI:
		var embeddings fwkrh.EmbeddingsRequest
		if err := json.Unmarshal(rawBody, &embeddings); err == nil && !embeddings.Input.IsEmpty() {
			return &fwkrh.InferenceRequestBody{Embeddings: &embeddings}, nil
		}
		return nil, errors.New("invalid embeddings request: must have input field")

	case imagesGenerationsAPI:
		var images fwkrh.ImagesGenerationsRequest
		if err := json.Unmarshal(rawBody, &images); err == nil && images.Prompt != "" {
			return &fwkrh.InferenceRequestBody{Images: &images}, nil
		}
		return nil, errors.New("invalid images generations request: must have prompt field")

	case audioSpeechAPI, audioTranscriptionsAPI, inferenceAPI:
		return &fwkrh.InferenceRequestBody{}, nil

	default:
		return nil, errors.New("unsupported API endpoint")
	}
}

func validateChatCompletionsMessages(messages []fwkrh.Message) error {
	if len(messages) == 0 {
		return errors.New("chat-completions request must have at least one message")
	}
	return nil
}

func extractUsage(responseBytes []byte) (*fwkrh.Usage, error) {
	var responseBody map[string]any
	if err := json.Unmarshal(responseBytes, &responseBody); err != nil {
		return nil, err
	}

	usageValue, ok := responseBody["usage"]
	if !ok || usageValue == nil {
		return nil, nil //nolint:nilnil
	}

	usg, ok := usageValue.(map[string]any)
	if !ok {
		return nil, nil //nolint:nilnil
	}

	objectType, _ := responseBody["object"].(string)
	usage := extractUsageByAPIType(usg, objectType)

	if promptTokenDetailsValue, ok := usg["prompt_tokens_details"]; ok && promptTokenDetailsValue != nil {
		if detailsMap, ok := promptTokenDetailsValue.(map[string]any); ok {
			if cachedTokensValue, ok := detailsMap["cached_tokens"]; ok {
				if cachedTokens, ok := toInt(cachedTokensValue); ok {
					usage.PromptTokenDetails = &fwkrh.PromptTokenDetails{CachedTokens: cachedTokens}
				}
			}
		}
	}

	if inputTokenDetailsValue, ok := usg["input_tokens_details"]; ok && inputTokenDetailsValue != nil {
		if detailsMap, ok := inputTokenDetailsValue.(map[string]any); ok {
			if cachedTokensValue, ok := detailsMap["cached_tokens"]; ok {
				if cachedTokens, ok := toInt(cachedTokensValue); ok {
					usage.PromptTokenDetails = &fwkrh.PromptTokenDetails{CachedTokens: cachedTokens}
				}
			}
		}
	}

	return &usage, nil
}

func extractUsageByAPIType(usg map[string]any, objectType string) fwkrh.Usage {
	usage := fwkrh.Usage{}
	switch {
	case strings.HasPrefix(objectType, objectTypeResponse) || strings.HasPrefix(objectType, objectTypeConversation):
		if v, ok := toInt(usg["input_tokens"]); ok {
			usage.PromptTokens = v
		}
		if v, ok := toInt(usg["output_tokens"]); ok {
			usage.CompletionTokens = v
		}
	case objectType == objectTypeChatCompletion || objectType == objectTypeChatCompletionChunk || objectType == objectTypeTemplateCompletion:
		if v, ok := toInt(usg["prompt_tokens"]); ok {
			usage.PromptTokens = v
		}
		if v, ok := toInt(usg["completion_tokens"]); ok {
			usage.CompletionTokens = v
		}
	default:
		if v, ok := toInt(usg["input_tokens"]); ok {
			usage.PromptTokens = v
		} else if v, ok := toInt(usg["prompt_tokens"]); ok {
			usage.PromptTokens = v
		}
		if v, ok := toInt(usg["output_tokens"]); ok {
			usage.CompletionTokens = v
		} else if v, ok := toInt(usg["completion_tokens"]); ok {
			usage.CompletionTokens = v
		}
	}

	if v, ok := toInt(usg["total_tokens"]); ok {
		usage.TotalTokens = v
	}
	return usage
}

func toInt(value any) (int, bool) {
	switch v := value.(type) {
	case nil:
		return 0, false
	case int:
		return v, true
	case int32:
		return int(v), true
	case int64:
		return int(v), true
	case float32:
		return int(v), true
	case float64:
		return int(v), true
	case json.Number:
		parsed, err := v.Int64()
		if err != nil {
			return 0, false
		}
		return int(parsed), true
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}

func extractUsageStreaming(responseText string) *fwkrh.Usage {
	var streamResponse struct {
		Usage    *fwkrh.Usage    `json:"usage"`
		Response json.RawMessage `json:"response"`
		Type     string          `json:"type"`
	}

	lines := strings.SplitSeq(responseText, "\n")
	for line := range lines {
		content, ok := strings.CutPrefix(line, streamingRespPrefix)
		if !ok {
			continue
		}
		if content == "[DONE]" || !strings.Contains(content, "usage") {
			continue
		}

		byteSlice := []byte(content)
		if err := json.Unmarshal(byteSlice, &streamResponse); err != nil {
			continue
		}
		if streamResponse.Usage != nil {
			return streamResponse.Usage
		}
		if len(streamResponse.Response) > 0 && streamResponse.Type == "response.completed" {
			if usage, err := extractUsage(streamResponse.Response); err == nil && usage != nil {
				return usage
			}
		}
	}
	return nil
}
