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
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateReportEmpty(t *testing.T) {
	var buf bytes.Buffer
	err := GenerateReport(&buf, nil)
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	assert.Len(t, lines, 2) // header + separator only
}

func TestGenerateReportSortsByComponentThenID(t *testing.T) {
	reqs := []Requirement{
		{ID: "B-002", Pattern: PatternUbiquitous, Text: "Second B", Component: "b"},
		{ID: "A-001", Pattern: PatternEventDriven, Text: "First A", Component: "a"},
		{ID: "B-001", Pattern: PatternUnwanted, Text: "First B", Component: "b"},
	}

	var buf bytes.Buffer
	err := GenerateReport(&buf, reqs)
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	assert.Len(t, lines, 5) // header + separator + 3 rows

	assert.Contains(t, lines[0], "ID")
	assert.Contains(t, lines[0], "Pattern")
	assert.Contains(t, lines[0], "Requirement")
	assert.Contains(t, lines[0], "Component")

	assert.Contains(t, lines[2], "A-001")
	assert.Contains(t, lines[3], "B-001")
	assert.Contains(t, lines[4], "B-002")
}

func TestGenerateReportContent(t *testing.T) {
	reqs := []Requirement{
		{ID: "X-001", Pattern: PatternStateDriven, Text: "While active, the system shall respond", Component: "core"},
	}

	var buf bytes.Buffer
	err := GenerateReport(&buf, reqs)
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "X-001")
	assert.Contains(t, output, "State-Driven")
	assert.Contains(t, output, "While active, the system shall respond")
	assert.Contains(t, output, "core")
}
