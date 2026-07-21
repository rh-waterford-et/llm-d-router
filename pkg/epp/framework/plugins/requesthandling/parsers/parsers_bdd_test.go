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

package parsers_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2" //nolint:revive
	. "github.com/onsi/gomega"    //nolint:revive

	fwkrh "github.com/llm-d/llm-d-router/pkg/epp/framework/interface/requesthandling"
	"github.com/llm-d/llm-d-router/pkg/epp/framework/plugins/requesthandling/parsers/anthropic"
	"github.com/llm-d/llm-d-router/pkg/epp/framework/plugins/requesthandling/parsers/openai"
	"github.com/llm-d/llm-d-router/test/ears"
)

var _ = Describe("Request Parsers", func() {
	var (
		oaiParser  fwkrh.Parser
		anthParser fwkrh.Parser
		ctx        context.Context
	)

	BeforeEach(func() {
		ctx = context.Background()
		oaiParser = openai.NewOpenAIParser()
		anthParser = anthropic.NewAnthropicParser()
	})

	Describe("OpenAI Parser", func() {
		When("parsing a valid completions request", func() {
			It("extracts the model and prompt", func() {
				ears.GinkgoRequirement(ears.ParseOAIModel)

				body := []byte(`{"model":"test-model","prompt":"hello world"}`)
				headers := map[string]string{":path": "/v1/completions"}
				result, err := oaiParser.ParseRequest(ctx, body, headers)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.Body).NotTo(BeNil())
				Expect(result.Body.Model).To(Equal("test-model"))

				payloadMap, ok := result.Body.Payload.(fwkrh.PayloadMap)
				Expect(ok).To(BeTrue(), "payload should be a PayloadMap")
				Expect(payloadMap["model"]).To(Equal("test-model"))
			})
		})

		When("parsing a chat completions request", func() {
			It("extracts messages from the body", func() {
				ears.GinkgoRequirement(ears.ParseOAIChat)

				body := []byte(`{"model":"test-model","messages":[{"role":"user","content":"hello"}]}`)
				headers := map[string]string{":path": "/v1/chat/completions"}
				result, err := oaiParser.ParseRequest(ctx, body, headers)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.Body).NotTo(BeNil())
				Expect(result.Body.ChatCompletions).NotTo(BeNil())
				Expect(result.Body.ChatCompletions.Messages).To(HaveLen(1))
				Expect(result.Body.ChatCompletions.Messages[0].Role).To(Equal("user"))
			})
		})

		When("receiving malformed JSON", func() {
			It("returns a parse error", func() {
				ears.GinkgoRequirement(ears.ParseOAIMalformed)

				body := []byte(`{not valid json}`)
				headers := map[string]string{":path": "/v1/completions"}
				_, err := oaiParser.ParseRequest(ctx, body, headers)
				Expect(err).To(HaveOccurred())
			})
		})

		When("receiving an empty body", func() {
			It("returns a parse error", func() {
				ears.GinkgoRequirement(ears.ParseOAIEmptyBody)

				headers := map[string]string{":path": "/v1/completions"}
				_, err := oaiParser.ParseRequest(ctx, []byte{}, headers)
				Expect(err).To(HaveOccurred())
			})
		})
	})

	Describe("Anthropic Parser", func() {
		When("parsing a valid messages request", func() {
			It("extracts the model name", func() {
				ears.GinkgoRequirement(ears.ParseAnthModel)

				body := []byte(`{"model":"claude-3","messages":[{"role":"user","content":"hello"}],"max_tokens":100}`)
				headers := map[string]string{":path": "/v1/messages"}
				result, err := anthParser.ParseRequest(ctx, body, headers)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.Body).NotTo(BeNil())
				Expect(result.Body.Model).To(Equal("claude-3"))

				payloadMap, ok := result.Body.Payload.(fwkrh.PayloadMap)
				Expect(ok).To(BeTrue(), "payload should be a PayloadMap")
				Expect(payloadMap["model"]).To(Equal("claude-3"))
			})
		})

		When("receiving malformed JSON", func() {
			It("returns a parse error", func() {
				ears.GinkgoRequirement(ears.ParseAnthMalformed)

				body := []byte(`{not valid}`)
				headers := map[string]string{":path": "/v1/messages"}
				_, err := anthParser.ParseRequest(ctx, body, headers)
				Expect(err).To(HaveOccurred())
			})
		})
	})
})
