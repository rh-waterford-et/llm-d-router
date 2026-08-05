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

package flowcontrol_test

import (
	"context"
	"sync"

	. "github.com/onsi/ginkgo/v2" //nolint:revive
	. "github.com/onsi/gomega"    //nolint:revive
	k8stypes "k8s.io/apimachinery/pkg/types"

	"github.com/llm-d/llm-d-router/pkg/epp/framework/interface/datalayer"
	"github.com/llm-d/llm-d-router/pkg/epp/framework/interface/flowcontrol"
	fwkplugin "github.com/llm-d/llm-d-router/pkg/epp/framework/interface/plugin"
	fwksched "github.com/llm-d/llm-d-router/pkg/epp/framework/interface/scheduling"
	concurrencydetector "github.com/llm-d/llm-d-router/pkg/epp/framework/plugins/flowcontrol/saturationdetector/concurrency"
	"github.com/llm-d/llm-d-router/test/ears"
)

var _ = Describe("Flow Control", func() {
	Describe("Saturation Detection", func() {
		var (
			detector flowcontrol.SaturationDetector
			ctx      context.Context
		)

		BeforeEach(func() {
			ctx = context.Background()

			plugin, err := concurrencydetector.ConcurrencyDetectorFactory(
				"bdd-test-detector",
				fwkplugin.StrictDecoder([]byte(`{"maxConcurrency": 10}`)),
				fwkplugin.NewEppHandle(ctx, func() []k8stypes.NamespacedName { return nil }),
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(plugin).NotTo(BeNil())

			var ok bool
			detector, ok = plugin.(flowcontrol.SaturationDetector)
			Expect(ok).To(BeTrue(), "plugin must implement SaturationDetector")
		})

		When("no requests are in flight", func() {
			It("reports zero saturation", func() {
				ears.GinkgoRequirement(ears.FCSaturationZero)

				// Create endpoints with no in-flight load attributes set.
				// The detector should treat missing load data as zero.
				ep := datalayer.NewEndpoint(
					&datalayer.EndpointMetadata{
						NamespacedName: k8stypes.NamespacedName{Name: "idle-pod", Namespace: "default"},
					},
					datalayer.NewMetrics(),
				)

				saturation := detector.Saturation(ctx, []datalayer.Endpoint{ep})
				Expect(saturation).To(BeNumerically("==", 0.0))
			})
		})

		When("no endpoints exist", func() {
			It("reports full saturation (fail-closed)", func() {
				ears.GinkgoRequirement(ears.FCSaturationFull)

				saturation := detector.Saturation(ctx, []datalayer.Endpoint{})
				Expect(saturation).To(BeNumerically("==", 1.0))
			})
		})

		When("checking saturation concurrently", func() {
			It("keeps saturation in [0.0, 1.0] for idle endpoints", func() {
				ears.GinkgoRequirement(ears.FCConcurrentSaturation)

				ep := datalayer.NewEndpoint(
					&datalayer.EndpointMetadata{
						NamespacedName: k8stypes.NamespacedName{Name: "concurrent-pod", Namespace: "default"},
					},
					datalayer.NewMetrics(),
				)
				endpoints := []datalayer.Endpoint{ep}

				const numGoroutines = 20
				const readsPerGoroutine = 100

				var wg sync.WaitGroup
				wg.Add(numGoroutines)

				violations := make(chan float64, numGoroutines*readsPerGoroutine)

				for range numGoroutines {
					go func() {
						defer wg.Done()
						for range readsPerGoroutine {
							sat := detector.Saturation(ctx, endpoints)
							if sat < 0.0 || sat > 1.0 {
								violations <- sat
							}
						}
					}()
				}

				wg.Wait()
				close(violations)

				Expect(violations).To(BeEmpty(), "all saturation reads should be within [0.0, 1.0]")
			})
		})
	})

	Describe("Filter Behavior", func() {
		When("an endpoint is below burst capacity", func() {
			It("retains the endpoint in the candidate list", func() {
				ears.GinkgoRequirement(ears.FCFilterOverloaded)

				ctx := context.Background()

				plugin, err := concurrencydetector.ConcurrencyDetectorFactory(
					"bdd-filter-test",
					fwkplugin.StrictDecoder([]byte(`{"maxConcurrency": 10, "headroom": 0.2}`)),
					fwkplugin.NewEppHandle(ctx, func() []k8stypes.NamespacedName { return nil }),
				)
				Expect(err).NotTo(HaveOccurred())

				filter, ok := plugin.(fwksched.Filter)
				Expect(ok).To(BeTrue(), "plugin must implement scheduling.Filter")

				// Create an endpoint with no in-flight load (zero requests).
				// Since 0 < maxConcurrency * (1 + headroom) = 12, it should pass the filter.
				ep := fwksched.NewEndpoint(
					&datalayer.EndpointMetadata{
						NamespacedName: k8stypes.NamespacedName{Name: "healthy-pod", Namespace: "default"},
					},
					datalayer.NewMetrics(),
					nil,
				)

				kept := filter.Filter(ctx, nil, []fwksched.Endpoint{ep})
				Expect(kept).To(HaveLen(1))
				Expect(kept[0].GetMetadata().NamespacedName.Name).To(Equal("healthy-pod"))
			})
		})
	})
})
