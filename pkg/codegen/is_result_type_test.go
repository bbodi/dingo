package codegen

import "testing"

// is_result_type_test.go — pin the matching rules for IsResultType.
// The original implementation accepted any type string that started
// with the six characters "Result", which spuriously matched real Go
// type names like `ResultPropBits` and `ResultType` and then triggered
// the validator to reject ordinary multi-return assignments as
// "cannot unpack Result[, ] as tuple". The fix requires a delimiter
// after `Result` (either `[` for generics or end-of-string for a
// bare unparameterised Result).

func TestIsResultType_PositiveCases(t *testing.T) {
	cases := []string{
		"Result[int, error]",
		"Result[T, E]",
		"dgo.Result[int, error]",
		"Result",            // bare — accepted; downstream code handles missing params
		"dgo.Result",        // bare with package prefix
		"Result[Foo, Bar]",  // nested-looking T/E
	}
	for _, s := range cases {
		if !IsResultType(s) {
			t.Errorf("IsResultType(%q) = false, want true", s)
		}
	}
}

func TestIsResultType_NegativeCases(t *testing.T) {
	// These are ORDINARY Go type names. They must not be mis-detected
	// as Result types, otherwise the result-tuple validator misfires.
	cases := []string{
		"ResultPropBits",   // hit by cmd/compile/internal/inline/inlheur
		"ResultType",
		"ResultantValue",
		"MyResult",
		"results",
		"resultMap",
		"int",
		"string",
		"",                  // empty string: not a Result
	}
	for _, s := range cases {
		if IsResultType(s) {
			t.Errorf("IsResultType(%q) = true, want false", s)
		}
	}
}

func TestIsResultType_DotQualified(t *testing.T) {
	// Dot-qualified non-Result types must not match.
	cases := []string{
		"pkg.ResultPropBits",
		"other.Resultant",
		"x.results",
	}
	for _, s := range cases {
		if IsResultType(s) {
			t.Errorf("IsResultType(%q) = true, want false", s)
		}
	}
}
