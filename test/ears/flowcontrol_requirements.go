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
	FCAdmitAtCapacity = Req("FC-ADMIT-001", PatternEventDriven,
		"When shard bytes are exactly at capacity, the admission controller shall reject the next request",
		"flowcontrol")
	FCAdmitUnderCapacity = Req("FC-ADMIT-002", PatternEventDriven,
		"When usage is below capacity, the admission controller shall admit the request",
		"flowcontrol")
	FCAdmitOverCapacity = Req("FC-ADMIT-003", PatternEventDriven,
		"When shard bytes exceed capacity, the admission controller shall reject the request",
		"flowcontrol")
	FCAdmitGlobalRejects = Req("FC-ADMIT-004", PatternStateDriven,
		"While global capacity is exhausted, the admission controller shall reject requests even when per-band has room",
		"flowcontrol")
	FCEvictNilSafety = Req("FC-EVICT-001", PatternUnwanted,
		"If the evictor receives a nil channel, then the evictor shall return without panic",
		"flowcontrol")
	FCEvictIdempotent = Req("FC-EVICT-002", PatternUnwanted,
		"If a request is evicted twice, then the evictor shall handle the second eviction idempotently",
		"flowcontrol")
	FCDeltaInvariant = Req("FC-DELTA-001", PatternUbiquitous,
		"The propagated length delta shall exactly match the change in queue size",
		"flowcontrol")
	FCConcurrentSaturation = Req("FC-SAT-001", PatternStateDriven,
		"While requests are being tracked and released concurrently, the saturation value shall remain in [0.0, 1.0]",
		"flowcontrol")
	FCSaturationZero = Req("FC-SAT-002", PatternEventDriven,
		"When all tracked requests are released, the saturation value shall return to exactly zero",
		"flowcontrol")
	FCSaturationFull = Req("FC-SAT-003", PatternEventDriven,
		"When no endpoints exist, the saturation detector shall report full saturation",
		"flowcontrol")
	FCFilterOverloaded = Req("FC-FILTER-001", PatternEventDriven,
		"When an endpoint is below burst capacity, the filter shall retain the endpoint in the candidate list",
		"flowcontrol")
)
