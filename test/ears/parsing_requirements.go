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

package ears

var (
	ParseOAIModel = Req("PARSE-OAI-001", PatternEventDriven,
		"When parsing a valid OpenAI completions request, the parser shall extract the model name",
		"parsing/openai")
	ParseOAIChat = Req("PARSE-OAI-002", PatternEventDriven,
		"When parsing a chat completions request, the parser shall extract messages from the body",
		"parsing/openai")
	ParseOAIMalformed = Req("PARSE-OAI-003", PatternUnwanted,
		"If the request body is not valid JSON, then the OpenAI parser shall return a parse error",
		"parsing/openai")
	ParseOAIEmptyBody = Req("PARSE-OAI-004", PatternUnwanted,
		"If the request body is empty, then the OpenAI parser shall return a parse error",
		"parsing/openai")
	ParseAnthModel = Req("PARSE-ANTH-001", PatternEventDriven,
		"When parsing an Anthropic messages request, the parser shall extract the model name",
		"parsing/anthropic")
	ParseAnthMalformed = Req("PARSE-ANTH-002", PatternUnwanted,
		"If the request body is not valid JSON, then the Anthropic parser shall return a parse error",
		"parsing/anthropic")
	ParseVertexModel = Req("PARSE-VTXAI-001", PatternEventDriven,
		"When parsing a VertexAI request, the parser shall extract the model name from the endpoint path",
		"parsing/vertexai")
	ParsePassthrough = Req("PARSE-PASS-001", PatternUbiquitous,
		"The passthrough parser shall forward request bodies without modification",
		"parsing/passthrough")
)
