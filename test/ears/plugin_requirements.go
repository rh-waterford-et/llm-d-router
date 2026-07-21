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
	PlugFilterNilEndpoints = Req("PLUG-FILTER-001", PatternEventDriven,
		"When given nil endpoints, every filter shall return an empty slice",
		"plugin/filter")
	PlugFilterEmptyEndpoints = Req("PLUG-FILTER-002", PatternEventDriven,
		"When given empty endpoints, every filter shall return an empty slice",
		"plugin/filter")
	PlugFilterAllMatch = Req("PLUG-FILTER-003", PatternEventDriven,
		"When all endpoints match the filter criteria, the filter shall return all endpoints",
		"plugin/filter")
	PlugScorerNilEndpoints = Req("PLUG-SCORER-001", PatternEventDriven,
		"When given nil endpoints, every scorer shall return an empty map",
		"plugin/scorer")
	PlugScorerRange = Req("PLUG-SCORER-002", PatternUbiquitous,
		"The scorer shall return score values within the range [0, 1]",
		"plugin/scorer")
	PlugPickerEmptyInput = Req("PLUG-PICKER-001", PatternEventDriven,
		"When given empty scored endpoints, the picker shall return a nil result",
		"plugin/picker")
)
