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

// Package ears provides EARS (Easy Approach to Requirements Syntax) requirement
// identifiers for test annotations.
package ears

import (
	"fmt"
	"sync"
	"testing"

	"github.com/onsi/ginkgo/v2"
)

// Pattern represents an EARS requirement pattern.
type Pattern string

const (
	PatternUbiquitous  Pattern = "Ubiquitous"
	PatternEventDriven Pattern = "Event-Driven"
	PatternUnwanted    Pattern = "Unwanted"
	PatternStateDriven Pattern = "State-Driven"
	PatternOptional    Pattern = "Optional"
)

// Requirement represents a single EARS requirement.
type Requirement struct {
	ID        string
	Pattern   Pattern
	Text      string
	Component string
}

var (
	mu       sync.Mutex
	registry []Requirement
)

// Register adds a requirement to the global registry.
func Register(req Requirement) {
	mu.Lock()
	defer mu.Unlock()
	registry = append(registry, req)
}

// All returns a copy of all registered requirements.
func All() []Requirement {
	mu.Lock()
	defer mu.Unlock()
	result := make([]Requirement, len(registry))
	copy(result, registry)
	return result
}

// Req creates a new Requirement and registers it in the global registry.
func Req(id string, pattern Pattern, text, component string) Requirement {
	req := Requirement{
		ID:        id,
		Pattern:   pattern,
		Text:      text,
		Component: component,
	}
	Register(req)
	return req
}

// TestRequirement logs the EARS requirement ID and text on a standard testing.TB.
func TestRequirement(t testing.TB, req Requirement) {
	t.Helper()
	t.Logf("EARS [%s]: %s", req.ID, req.Text)
}

// GinkgoRequirement annotates the current Ginkgo spec with the EARS requirement.
func GinkgoRequirement(req Requirement) {
	ginkgo.By(fmt.Sprintf("EARS [%s]: %s", req.ID, req.Text))
}
