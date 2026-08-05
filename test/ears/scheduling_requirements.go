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
	SchedNoEndpoints = Req("SCHED-ROUTE-001", PatternEventDriven,
		"When no candidate endpoints exist, the scheduler shall return an error",
		"scheduling")
	SchedAllFiltered = Req("SCHED-ROUTE-002", PatternEventDriven,
		"When all filters eliminate every endpoint, the scheduler shall return an error",
		"scheduling")
	SchedSelectHighest = Req("SCHED-ROUTE-003", PatternUbiquitous,
		"The scheduler shall route requests to the highest-scored endpoint",
		"scheduling")
	SchedScoreRange = Req("SCHED-SCORER-001", PatternUbiquitous,
		"The scheduler shall clamp endpoint scores to the range [0, 1]",
		"scheduling")
	SchedPickerEmpty = Req("SCHED-PICKER-001", PatternEventDriven,
		"When given empty scored endpoints, the picker shall return a nil result",
		"scheduling")
	SchedProfileSelection = Req("SCHED-PROFILE-001", PatternEventDriven,
		"When multiple scheduler profiles are configured, the profile handler shall select which profiles to run",
		"scheduling")
	SchedSingleEndpoint = Req("SCHED-ROUTE-004", PatternEventDriven,
		"When a single endpoint is available, the scheduler shall select that endpoint",
		"scheduling")
)
