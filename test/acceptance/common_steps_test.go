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

	"github.com/cucumber/godog"
)

type contextKey string

const (
	keyScheduler      contextKey = "scheduler"
	keyEndpoints      contextKey = "endpoints"
	keyRequest        contextKey = "request"
	keyResult         contextKey = "result"
	keyError          contextKey = "error"
	keyParseResult    contextKey = "parseResult"
	keyRequestBody    contextKey = "requestBody"
	keyRequestHeaders contextKey = "requestHeaders"
	keyDetector       contextKey = "detector"
	keySaturation     contextKey = "saturation"
	keySaturationAll  contextKey = "saturationAll"
)

func InitializeScenario(ctx *godog.ScenarioContext) {
	initSchedulingSteps(ctx)
	initParsingSteps(ctx)
	initFlowControlSteps(ctx)
}

func withValue(ctx context.Context, key contextKey, val any) context.Context {
	return context.WithValue(ctx, key, val)
}

func getValue[T any](ctx context.Context, key contextKey) T {
	val, _ := ctx.Value(key).(T)
	return val
}
