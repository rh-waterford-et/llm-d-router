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

package acceptance_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/cucumber/godog"

	fwkrh "github.com/llm-d/llm-d-router/pkg/epp/framework/interface/requesthandling"
	"github.com/llm-d/llm-d-router/pkg/epp/framework/plugins/requesthandling/parsers/anthropic"
	"github.com/llm-d/llm-d-router/pkg/epp/framework/plugins/requesthandling/parsers/openai"
)

func initParsingSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^an OpenAI completions request with model "([^"]*)" and prompt "([^"]*)"$`, openAICompletionsRequest)
	ctx.Step(`^an OpenAI chat completions request with model "([^"]*)" and message "([^"]*)"$`, openAIChatCompletionsRequest)
	ctx.Step(`^an OpenAI chat completions request with (\d+) messages$`, openAIChatCompletionsMultipleMessages)
	ctx.Step(`^a request body that is not valid JSON$`, malformedJSONRequestBody)
	ctx.Step(`^an empty request body$`, emptyRequestBody)
	ctx.Step(`^the request is parsed$`, theRequestIsParsedOpenAI)
	ctx.Step(`^the OpenAI parser attempts to parse the request$`, theRequestIsParsedOpenAI)
	ctx.Step(`^the parsed model is "([^"]*)"$`, parsedModelIs)
	ctx.Step(`^the parsed prompt contains "([^"]*)"$`, parsedPromptContains)
	ctx.Step(`^the parsed body contains messages$`, parsedBodyContainsMessages)
	ctx.Step(`^parsing returns an error$`, parsingReturnsAnError)

	ctx.Step(`^an Anthropic messages request with model "([^"]*)" and message "([^"]*)"$`, anthropicMessagesRequest)
	ctx.Step(`^the Anthropic parser parses the request$`, theRequestIsParsedAnthropic)
	ctx.Step(`^the Anthropic parser attempts to parse the request$`, theRequestIsParsedAnthropic)
}

func openAICompletionsRequest(ctx context.Context, model, prompt string) (context.Context, error) {
	body := map[string]any{
		"model":  model,
		"prompt": prompt,
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return ctx, err
	}
	headers := map[string]string{":path": "/v1/completions"}
	ctx = withValue(ctx, keyRequestBody, bodyBytes)
	ctx = withValue(ctx, keyRequestHeaders, headers)
	return ctx, nil
}

func openAIChatCompletionsRequest(ctx context.Context, model, message string) (context.Context, error) {
	body := map[string]any{
		"model": model,
		"messages": []map[string]any{
			{"role": "user", "content": message},
		},
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return ctx, err
	}
	headers := map[string]string{":path": "/v1/chat/completions"}
	ctx = withValue(ctx, keyRequestBody, bodyBytes)
	ctx = withValue(ctx, keyRequestHeaders, headers)
	return ctx, nil
}

func openAIChatCompletionsMultipleMessages(ctx context.Context, count int) (context.Context, error) {
	messages := make([]map[string]any, 0, count)
	for i := range count {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		messages = append(messages, map[string]any{
			"role":    role,
			"content": fmt.Sprintf("message %d", i+1),
		})
	}
	body := map[string]any{
		"model":    "test-model",
		"messages": messages,
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return ctx, err
	}
	headers := map[string]string{":path": "/v1/chat/completions"}
	ctx = withValue(ctx, keyRequestBody, bodyBytes)
	ctx = withValue(ctx, keyRequestHeaders, headers)
	return ctx, nil
}

func malformedJSONRequestBody(ctx context.Context) (context.Context, error) {
	ctx = withValue(ctx, keyRequestBody, []byte(`{not valid json}`))
	ctx = withValue(ctx, keyRequestHeaders, map[string]string{":path": "/v1/completions"})
	return ctx, nil
}

func emptyRequestBody(ctx context.Context) (context.Context, error) {
	ctx = withValue(ctx, keyRequestBody, []byte{})
	ctx = withValue(ctx, keyRequestHeaders, map[string]string{":path": "/v1/completions"})
	return ctx, nil
}

func theRequestIsParsedOpenAI(ctx context.Context) (context.Context, error) {
	parser := openai.NewOpenAIParser()
	body := getValue[[]byte](ctx, keyRequestBody)
	headers := getValue[map[string]string](ctx, keyRequestHeaders)

	result, err := parser.ParseRequest(ctx, body, headers)
	ctx = withValue(ctx, keyParseResult, result)
	ctx = withValue(ctx, keyError, err)
	return ctx, nil
}

func theRequestIsParsedAnthropic(ctx context.Context) (context.Context, error) {
	parser := anthropic.NewAnthropicParser()
	body := getValue[[]byte](ctx, keyRequestBody)
	headers := getValue[map[string]string](ctx, keyRequestHeaders)

	result, err := parser.ParseRequest(ctx, body, headers)
	ctx = withValue(ctx, keyParseResult, result)
	ctx = withValue(ctx, keyError, err)
	return ctx, nil
}

func parsedModelIs(ctx context.Context, expectedModel string) error {
	result := getValue[*fwkrh.ParseResult](ctx, keyParseResult)
	if result == nil || result.Body == nil {
		return errors.New("no parse result available")
	}
	if result.Body.Model != expectedModel {
		return fmt.Errorf("expected model %q, got %q", expectedModel, result.Body.Model)
	}
	return nil
}

func parsedPromptContains(ctx context.Context, expected string) error {
	result := getValue[*fwkrh.ParseResult](ctx, keyParseResult)
	if result == nil || result.Body == nil {
		return errors.New("no parse result available")
	}
	if result.Body.Completions == nil {
		return errors.New("parse result has no completions")
	}
	if result.Body.Completions.Prompt.Raw != expected {
		return fmt.Errorf("expected prompt %q, got %q", expected, result.Body.Completions.Prompt.Raw)
	}
	return nil
}

func parsedBodyContainsMessages(ctx context.Context) error {
	result := getValue[*fwkrh.ParseResult](ctx, keyParseResult)
	if result == nil || result.Body == nil {
		return errors.New("no parse result available")
	}
	if result.Body.ChatCompletions == nil {
		return errors.New("parse result has no chat completions")
	}
	if len(result.Body.ChatCompletions.Messages) == 0 {
		return errors.New("chat completions has no messages")
	}
	return nil
}

func parsingReturnsAnError(ctx context.Context) error {
	err := getValue[error](ctx, keyError)
	if err == nil {
		return errors.New("expected a parse error, got nil")
	}
	return nil
}

func anthropicMessagesRequest(ctx context.Context, model, message string) (context.Context, error) {
	body := map[string]any{
		"model":      model,
		"max_tokens": 100,
		"messages": []map[string]any{
			{"role": "user", "content": message},
		},
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return ctx, err
	}
	headers := map[string]string{":path": "/v1/messages"}
	ctx = withValue(ctx, keyRequestBody, bodyBytes)
	ctx = withValue(ctx, keyRequestHeaders, headers)
	return ctx, nil
}
