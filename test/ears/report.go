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
	"fmt"
	"io"
	"sort"
)

// GenerateReport writes a Markdown table of requirements to w, sorted by
// component then ID.
func GenerateReport(w io.Writer, reqs []Requirement) error {
	sorted := make([]Requirement, len(reqs))
	copy(sorted, reqs)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Component != sorted[j].Component {
			return sorted[i].Component < sorted[j].Component
		}
		return sorted[i].ID < sorted[j].ID
	})

	if _, err := fmt.Fprintln(w, "| ID | Pattern | Requirement | Component |"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "|----|---------|-------------|-----------|"); err != nil {
		return err
	}
	for _, req := range sorted {
		if _, err := fmt.Fprintf(w, "| %s | %s | %s | %s |\n", req.ID, req.Pattern, req.Text, req.Component); err != nil {
			return err
		}
	}
	return nil
}
