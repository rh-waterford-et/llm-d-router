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
	"fmt"

	"github.com/cucumber/godog"
	"github.com/google/uuid"
	k8stypes "k8s.io/apimachinery/pkg/types"

	fwkdl "github.com/llm-d/llm-d-router/pkg/epp/framework/interface/datalayer"
	fwksched "github.com/llm-d/llm-d-router/pkg/epp/framework/interface/scheduling"
	"github.com/llm-d/llm-d-router/pkg/epp/framework/plugins/scheduling/picker"
	"github.com/llm-d/llm-d-router/pkg/epp/framework/plugins/scheduling/picker/maxscore"
	"github.com/llm-d/llm-d-router/pkg/epp/framework/plugins/scheduling/profilehandler/single"
	"github.com/llm-d/llm-d-router/pkg/epp/framework/plugins/scheduling/scorer/kvcacheutilization"
	"github.com/llm-d/llm-d-router/pkg/epp/framework/plugins/scheduling/scorer/queuedepth"
	"github.com/llm-d/llm-d-router/pkg/epp/scheduling"
)

func initSchedulingSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^a scheduler with default configuration$`, aSchedulerWithDefaultConfiguration)
	ctx.Step(`^(\d+) endpoints with queue depths (\d+), (\d+), and (\d+)$`, endpointsWithQueueDepths3)
	ctx.Step(`^(\d+) endpoints$`, nEndpoints)
	ctx.Step(`^(\d+) endpoint with queue depth (\d+)$`, singleEndpointWithQueueDepth)
	ctx.Step(`^a completion request arrives for model "([^"]*)"$`, aCompletionRequestArrives)
	ctx.Step(`^the request is routed to the endpoint with queue depth (\d+)$`, requestRoutedToEndpointWithQueueDepth)
	ctx.Step(`^the scheduler returns an error$`, schedulerReturnsAnError)
	ctx.Step(`^the request is routed to the only available endpoint$`, requestRoutedToOnlyEndpoint)
	ctx.Step(`^the request is routed to one of the endpoints$`, requestRoutedToOneOfEndpoints)
	ctx.Step(`^(\d+) endpoints that do not match the filter criteria$`, endpointsThatDoNotMatch)
	ctx.Step(`^(\d+) endpoints where (\d+) matches the filter criteria$`, endpointsWhereSomeMatch)
	ctx.Step(`^the request is routed to the matching endpoint$`, requestRoutedToMatchingEndpoint)
}

func aSchedulerWithDefaultConfiguration(ctx context.Context) (context.Context, error) {
	kvCacheScorer := kvcacheutilization.NewKVCacheUtilizationScorer()
	queueScorer := queuedepth.NewQueueScorer()

	defaultProfile := scheduling.NewSchedulerProfile().
		WithScorers(
			scheduling.NewWeightedScorer(kvCacheScorer, 1),
			scheduling.NewWeightedScorer(queueScorer, 1),
		).
		WithPicker(maxscore.NewMaxScorePicker(picker.DefaultMaxNumOfEndpoints))

	profileHandler := single.NewSingleProfileHandler()
	config := scheduling.NewSchedulerConfig(profileHandler, map[string]fwksched.SchedulerProfile{"default": defaultProfile})
	s := scheduling.NewSchedulerWithConfig(config)

	return withValue(ctx, keyScheduler, s), nil
}

func endpointsWithQueueDepths3(ctx context.Context, count, q1, q2, q3 int) (context.Context, error) {
	if count != 3 {
		return ctx, fmt.Errorf("expected 3 endpoints, got %d", count)
	}
	endpoints := []fwksched.Endpoint{
		makeEndpoint("pod1", q1, 0.2),
		makeEndpoint("pod2", q2, 0.2),
		makeEndpoint("pod3", q3, 0.2),
	}
	return withValue(ctx, keyEndpoints, endpoints), nil
}

func nEndpoints(ctx context.Context, count int) (context.Context, error) {
	endpoints := make([]fwksched.Endpoint, 0, count)
	for i := range count {
		endpoints = append(endpoints, makeEndpoint(fmt.Sprintf("pod%d", i+1), i*2, 0.2))
	}
	return withValue(ctx, keyEndpoints, endpoints), nil
}

func singleEndpointWithQueueDepth(ctx context.Context, count, queueDepth int) (context.Context, error) {
	if count != 1 {
		return ctx, fmt.Errorf("expected 1 endpoint, got %d", count)
	}
	endpoints := []fwksched.Endpoint{
		makeEndpoint("pod1", queueDepth, 0.2),
	}
	return withValue(ctx, keyEndpoints, endpoints), nil
}

func aCompletionRequestArrives(ctx context.Context, model string) (context.Context, error) {
	s := getValue[*scheduling.Scheduler](ctx, keyScheduler)
	endpoints := getValue[[]fwksched.Endpoint](ctx, keyEndpoints)

	req := &fwksched.InferenceRequest{
		RequestID:   uuid.NewString(),
		TargetModel: model,
	}

	result, err := s.Schedule(ctx, req, endpoints)
	ctx = withValue(ctx, keyResult, result)
	ctx = withValue(ctx, keyError, err)
	return ctx, nil
}

func requestRoutedToEndpointWithQueueDepth(ctx context.Context, expectedQueueDepth int) error {
	result := getValue[*fwksched.SchedulingResult](ctx, keyResult)
	if result == nil {
		return fmt.Errorf("expected a scheduling result, got nil")
	}

	primary := result.ProfileResults[result.PrimaryProfileName]
	if primary == nil || len(primary.TargetEndpoints) == 0 {
		return fmt.Errorf("no target endpoints in result")
	}

	target := primary.TargetEndpoints[0]
	metrics := target.GetMetrics()
	if metrics == nil {
		return fmt.Errorf("target endpoint has no metrics")
	}
	if metrics.WaitingQueueSize != expectedQueueDepth {
		return fmt.Errorf("expected queue depth %d, got %d", expectedQueueDepth, metrics.WaitingQueueSize)
	}
	return nil
}

func schedulerReturnsAnError(ctx context.Context) error {
	err := getValue[error](ctx, keyError)
	if err == nil {
		return fmt.Errorf("expected an error, got nil")
	}
	return nil
}

func requestRoutedToOnlyEndpoint(ctx context.Context) error {
	result := getValue[*fwksched.SchedulingResult](ctx, keyResult)
	if result == nil {
		return fmt.Errorf("expected a scheduling result, got nil")
	}

	primary := result.ProfileResults[result.PrimaryProfileName]
	if primary == nil || len(primary.TargetEndpoints) == 0 {
		return fmt.Errorf("no target endpoints in result")
	}
	return nil
}

func requestRoutedToOneOfEndpoints(ctx context.Context) error {
	result := getValue[*fwksched.SchedulingResult](ctx, keyResult)
	if result == nil {
		return fmt.Errorf("expected a scheduling result, got nil")
	}

	primary := result.ProfileResults[result.PrimaryProfileName]
	if primary == nil || len(primary.TargetEndpoints) == 0 {
		return fmt.Errorf("no target endpoints in result")
	}
	return nil
}

func endpointsThatDoNotMatch(ctx context.Context, count int) (context.Context, error) {
	endpoints := make([]fwksched.Endpoint, 0, count)
	for i := range count {
		endpoints = append(endpoints, makeEndpoint(fmt.Sprintf("pod%d", i+1), 0, 0.2))
	}
	return withValue(ctx, keyEndpoints, endpoints), nil
}

func endpointsWhereSomeMatch(ctx context.Context, total, matching int) (context.Context, error) {
	endpoints := make([]fwksched.Endpoint, 0, total)
	for i := range total {
		qd := 0
		if i < matching {
			qd = 1
		}
		endpoints = append(endpoints, makeEndpoint(fmt.Sprintf("pod%d", i+1), qd, 0.2))
	}
	return withValue(ctx, keyEndpoints, endpoints), nil
}

func requestRoutedToMatchingEndpoint(ctx context.Context) error {
	result := getValue[*fwksched.SchedulingResult](ctx, keyResult)
	if result == nil {
		return fmt.Errorf("expected a scheduling result, got nil")
	}

	primary := result.ProfileResults[result.PrimaryProfileName]
	if primary == nil || len(primary.TargetEndpoints) == 0 {
		return fmt.Errorf("no target endpoints in result")
	}
	return nil
}

func makeEndpoint(name string, queueDepth int, kvCachePercent float64) fwksched.Endpoint {
	return fwksched.NewEndpoint(
		&fwkdl.EndpointMetadata{NamespacedName: k8stypes.NamespacedName{Name: name}},
		&fwkdl.Metrics{
			WaitingQueueSize:    queueDepth,
			KVCacheUsagePercent: kvCachePercent,
			MaxActiveModels:     2,
			ActiveModels:        map[string]int{},
		}, nil,
	)
}
