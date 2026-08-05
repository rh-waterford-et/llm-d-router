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

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestReqCreatesRequirement(t *testing.T) {
	// Reset registry for isolation
	mu.Lock()
	registry = nil
	mu.Unlock()

	req := Req("TEST-001", PatternUbiquitous, "The system shall do something", "test")

	assert.Equal(t, "TEST-001", req.ID)
	assert.Equal(t, PatternUbiquitous, req.Pattern)
	assert.Equal(t, "The system shall do something", req.Text)
	assert.Equal(t, "test", req.Component)
}

func TestReqRegistersInRegistry(t *testing.T) {
	mu.Lock()
	registry = nil
	mu.Unlock()

	Req("TEST-002", PatternEventDriven, "When triggered, the system shall respond", "test")

	all := All()
	assert.Len(t, all, 1)
	assert.Equal(t, "TEST-002", all[0].ID)
}

func TestAllReturnsAllRegistered(t *testing.T) {
	mu.Lock()
	registry = nil
	mu.Unlock()

	Req("TEST-003", PatternUbiquitous, "Requirement one", "a")
	Req("TEST-004", PatternUnwanted, "Requirement two", "b")
	Req("TEST-005", PatternStateDriven, "Requirement three", "c")

	all := All()
	assert.Len(t, all, 3)
	assert.Equal(t, "TEST-003", all[0].ID)
	assert.Equal(t, "TEST-004", all[1].ID)
	assert.Equal(t, "TEST-005", all[2].ID)
}

func TestTestRequirementDoesNotPanic(t *testing.T) {
	req := Requirement{
		ID:        "TEST-006",
		Pattern:   PatternOptional,
		Text:      "Where feature is enabled, the system shall behave",
		Component: "test",
	}

	assert.NotPanics(t, func() {
		TestRequirement(t, req)
	})
}

func TestPatternConstants(t *testing.T) {
	assert.Equal(t, Pattern("Ubiquitous"), PatternUbiquitous)
	assert.Equal(t, Pattern("Event-Driven"), PatternEventDriven)
	assert.Equal(t, Pattern("Unwanted"), PatternUnwanted)
	assert.Equal(t, Pattern("State-Driven"), PatternStateDriven)
	assert.Equal(t, Pattern("Optional"), PatternOptional)
}
