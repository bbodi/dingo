package transpiler

import (
	"strings"
	"testing"
)

// TestErrorPropPlainAssignNoShadow verifies that `varName = expr?` does not
// shadow an outer variable when the enclosing function has an unnamed error
// return.
//
// Regression: previously the transpiler emitted `varName := tmp` for the
// trailing assignment whenever there was no named error return, which
// shadowed any outer `var varName T` declaration and silently dropped the
// computed value.
func TestErrorPropPlainAssignNoShadow(t *testing.T) {
	tests := []struct {
		name           string
		source         string
		outputContains []string
		outputExcludes []string
	}{
		{
			name: "plain assign in if-else with outer var (unnamed error return)",
			source: `package main

import "strconv"

func process() (int, error) {
	var result int
	if true {
		result = strconv.Atoi("42") ? "first failed"
	} else {
		result = strconv.Atoi("0") ? "second failed"
	}
	return result, nil
}
`,
			// Both branches should assign through to the outer ` result` via
			// plain `=` — never shadow with `:=`.
			outputContains: []string{
				"result = tmp\n",
				"result = tmp1\n",
			},
			outputExcludes: []string{
				"result := tmp",
				"result := tmp1",
			},
		},
		{
			name: "plain assign in if (single branch) with outer var",
			source: `package main

import "strconv"

func parse(s string) (int, error) {
	var n int
	if s != "" {
		n = strconv.Atoi(s) ? "parse failed"
	}
	return n, nil
}
`,
			outputContains: []string{"n = tmp\n"},
			outputExcludes: []string{"n := tmp"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := PureASTTranspile([]byte(tt.source), "test_plain_assign.dingo")
			if err != nil {
				t.Fatalf("expected successful transpilation, got error: %v\noutput:\n%s",
					err, string(result))
			}
			goCode := string(result)
			for _, substr := range tt.outputContains {
				if !strings.Contains(goCode, substr) {
					t.Errorf("output should contain %q, got:\n%s", substr, goCode)
				}
			}
			for _, substr := range tt.outputExcludes {
				if strings.Contains(goCode, substr) {
					t.Errorf("output should NOT contain %q (shadow bug), got:\n%s", substr, goCode)
				}
			}
		})
	}
}
