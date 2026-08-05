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

package scheduling_test

import (
	"context"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2" //nolint:revive
	. "github.com/onsi/gomega"    //nolint:revive
	k8stypes "k8s.io/apimachinery/pkg/types"

	"github.com/llm-d/llm-d-router/pkg/epp/framework/interface/datalayer"
	fwkplugin "github.com/llm-d/llm-d-router/pkg/epp/framework/interface/plugin"
	fwksched "github.com/llm-d/llm-d-router/pkg/epp/framework/interface/scheduling"
	"github.com/llm-d/llm-d-router/pkg/epp/framework/plugins/scheduling/picker"
	"github.com/llm-d/llm-d-router/pkg/epp/framework/plugins/scheduling/picker/maxscore"
	"github.com/llm-d/llm-d-router/pkg/epp/framework/plugins/scheduling/profilehandler/single"
	"github.com/llm-d/llm-d-router/pkg/epp/framework/plugins/scheduling/scorer/kvcacheutilization"
	"github.com/llm-d/llm-d-router/pkg/epp/framework/plugins/scheduling/scorer/queuedepth"
	"github.com/llm-d/llm-d-router/pkg/epp/scheduling"
	"github.com/llm-d/llm-d-router/test/ears"
)

// rejectAllFilter is a scheduling filter that rejects every endpoint.
type rejectAllFilter struct{}

func (f *rejectAllFilter) TypedName() fwkplugin.TypedName {
	return fwkplugin.TypedName{Type: "reject-all-filter", Name: "reject-all-filter"}
}

func (f *rejectAllFilter) Filter(_ context.Context, _ *fwksched.InferenceRequest, _ []fwksched.Endpoint) []fwksched.Endpoint {
	return nil
}

var _ = Describe("Scheduler", func() {
	var (
		scheduler *scheduling.Scheduler
		ctx       context.Context
	)

	BeforeEach(func() {
		ctx = context.Background()

		kvCacheScorer := kvcacheutilization.NewKVCacheUtilizationScorer()
		queueScorer := queuedepth.NewQueueScorer()

		profile := scheduling.NewSchedulerProfile().
			WithScorers(
				scheduling.NewWeightedScorer(kvCacheScorer, 1),
				scheduling.NewWeightedScorer(queueScorer, 1),
			).
			WithPicker(maxscore.NewMaxScorePicker(picker.DefaultMaxNumOfEndpoints))

		profileHandler := single.NewSingleProfileHandler()
		config := scheduling.NewSchedulerConfig(profileHandler, map[string]fwksched.SchedulerProfile{"default": profile})
		scheduler = scheduling.NewSchedulerWithConfig(config)
	})

	When("no candidate endpoints exist", func() {
		It("returns an error", func() {
			ears.GinkgoRequirement(ears.SchedNoEndpoints)

			req := &fwksched.InferenceRequest{
				RequestID:   uuid.NewString(),
				TargetModel: "any-model",
			}
			result, err := scheduler.Schedule(ctx, req, []fwksched.Endpoint{})
			Expect(err).To(HaveOccurred())
			Expect(result).To(BeNil())
		})
	})

	When("a single endpoint is available", func() {
		It("selects that endpoint", func() {
			ears.GinkgoRequirement(ears.SchedSingleEndpoint)

			ep := fwksched.NewEndpoint(
				&datalayer.EndpointMetadata{NamespacedName: k8stypes.NamespacedName{Name: "solo-pod"}},
				&datalayer.Metrics{
					WaitingQueueSize:    0,
					KVCacheUsagePercent: 0.1,
					MaxActiveModels:     2,
					ActiveModels:        map[string]int{"test-model": 1},
				}, nil)

			req := &fwksched.InferenceRequest{
				RequestID:   uuid.NewString(),
				TargetModel: "test-model",
			}
			result, err := scheduler.Schedule(ctx, req, []fwksched.Endpoint{ep})
			Expect(err).NotTo(HaveOccurred())
			Expect(result).NotTo(BeNil())
			Expect(result.ProfileResults).To(HaveKey("default"))
			Expect(result.ProfileResults["default"].TargetEndpoints).To(HaveLen(1))
			Expect(result.ProfileResults["default"].TargetEndpoints[0].GetMetadata().NamespacedName.Name).To(Equal("solo-pod"))
		})
	})

	When("multiple endpoints with different scores", func() {
		It("selects the highest scored endpoint", func() {
			ears.GinkgoRequirement(ears.SchedSelectHighest)

			// pod1: moderate queue, moderate KV cache
			ep1 := fwksched.NewEndpoint(
				&datalayer.EndpointMetadata{NamespacedName: k8stypes.NamespacedName{Name: "pod1"}},
				&datalayer.Metrics{
					WaitingQueueSize:    5,
					KVCacheUsagePercent: 0.5,
					MaxActiveModels:     2,
					ActiveModels:        map[string]int{"foo": 1},
				}, nil)

			// pod2: lowest queue + lowest KV cache = best candidate
			ep2 := fwksched.NewEndpoint(
				&datalayer.EndpointMetadata{NamespacedName: k8stypes.NamespacedName{Name: "pod2"}},
				&datalayer.Metrics{
					WaitingQueueSize:    0,
					KVCacheUsagePercent: 0.1,
					MaxActiveModels:     2,
					ActiveModels:        map[string]int{"foo": 1},
				}, nil)

			// pod3: high queue, high KV cache
			ep3 := fwksched.NewEndpoint(
				&datalayer.EndpointMetadata{NamespacedName: k8stypes.NamespacedName{Name: "pod3"}},
				&datalayer.Metrics{
					WaitingQueueSize:    10,
					KVCacheUsagePercent: 0.9,
					MaxActiveModels:     2,
					ActiveModels:        map[string]int{"foo": 1},
				}, nil)

			req := &fwksched.InferenceRequest{
				RequestID:   uuid.NewString(),
				TargetModel: "test-model",
			}
			result, err := scheduler.Schedule(ctx, req, []fwksched.Endpoint{ep1, ep2, ep3})
			Expect(err).NotTo(HaveOccurred())
			Expect(result).NotTo(BeNil())
			Expect(result.ProfileResults["default"].TargetEndpoints).To(HaveLen(1))
			// pod2 has the lowest queue depth and lowest KV cache, so it should score highest
			Expect(result.ProfileResults["default"].TargetEndpoints[0].GetMetadata().NamespacedName.Name).To(Equal("pod2"))
		})
	})

	When("all filters eliminate every endpoint", func() {
		It("returns an error", func() {
			ears.GinkgoRequirement(ears.SchedAllFiltered)

			// Build a profile with a filter that rejects all endpoints
			kvCacheScorer := kvcacheutilization.NewKVCacheUtilizationScorer()
			profile := scheduling.NewSchedulerProfile().
				WithFilters(&rejectAllFilter{}).
				WithScorers(scheduling.NewWeightedScorer(kvCacheScorer, 1)).
				WithPicker(maxscore.NewMaxScorePicker(picker.DefaultMaxNumOfEndpoints))

			profileHandler := single.NewSingleProfileHandler()
			config := scheduling.NewSchedulerConfig(profileHandler, map[string]fwksched.SchedulerProfile{"default": profile})
			filteredScheduler := scheduling.NewSchedulerWithConfig(config)

			ep := fwksched.NewEndpoint(
				&datalayer.EndpointMetadata{NamespacedName: k8stypes.NamespacedName{Name: "pod1"}},
				&datalayer.Metrics{
					WaitingQueueSize:    0,
					KVCacheUsagePercent: 0.2,
					MaxActiveModels:     2,
					ActiveModels:        map[string]int{"foo": 1},
				}, nil)

			req := &fwksched.InferenceRequest{
				RequestID:   uuid.NewString(),
				TargetModel: "test-model",
			}
			result, err := filteredScheduler.Schedule(ctx, req, []fwksched.Endpoint{ep})
			Expect(err).To(HaveOccurred())
			Expect(result).To(BeNil())
		})
	})
})
