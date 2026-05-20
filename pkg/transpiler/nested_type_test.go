package transpiler

import (
	"strings"
	"testing"
)

// nested_type_test.go — regression tests for parser robustness on
// nested type expressions that the Dingo Pratt parser does not fully
// model. These types parse fine through Go's parser later in the
// pipeline; the only requirement is that the StmtParser-driven
// statement scan (used for tuple/match detection) does not *panic*.
//
// The original panic was in parseIndexExpr: when its `left` argument
// arrived nil (because an earlier prefix parselet bailed out without
// adding an error), `left.String()` segfaulted. Triggered most often
// by `map[K][N]V` style type expressions, where the array `[N]` was
// mistaken for an index operator on the partially-parsed map value
// position. Six files in cmd/compile hit this in the wild.
//
// The fix is defensive: the parselet guards against a nil left and
// returns nil cleanly. Downstream the Go parser handles the source
// correctly because the StmtParser's only job here is to *find*
// Dingo-specific expressions, not to faithfully parse all Go syntax.

func TestNestedType_MapOfArray_NoPanic(t *testing.T) {
	src := `package x

var a = make(map[int][2]int64)
`
	out, err := PureASTTranspile([]byte(src), "")
	if err != nil {
		t.Fatalf("transpile failed: %v", err)
	}
	if !strings.Contains(string(out), "map[int][2]int64") {
		t.Errorf("nested map/array type lost.\n%s", out)
	}
}

func TestNestedType_MapOfMap_NoPanic(t *testing.T) {
	// Same shape: a `[` immediately following a `]` triggers the
	// problematic infix path. Map-of-map is the natural cousin.
	src := `package x

var a = make(map[int]map[string]int)
`
	out, err := PureASTTranspile([]byte(src), "")
	if err != nil {
		t.Fatalf("transpile failed: %v", err)
	}
	if !strings.Contains(string(out), "map[int]map[string]int") {
		t.Errorf("nested map type lost.\n%s", out)
	}
}

func TestNestedType_ArrayValue_StructField(t *testing.T) {
	// Same panic appears inside a struct field of a fixed-size array
	// value type — covers `typeSymIdx = make(map[*types.Type][2]int64)`
	// from typecheck/iimport.dingo.
	src := `package x

type Type struct{}

var typeSymIdx = make(map[*Type][2]int64)
`
	out, err := PureASTTranspile([]byte(src), "")
	if err != nil {
		t.Fatalf("transpile failed: %v", err)
	}
	if !strings.Contains(string(out), "map[*Type][2]int64") {
		t.Errorf("map-pointer-array type lost.\n%s", out)
	}
}

func TestParseIndexExpr_NilLeft_NoPanic(t *testing.T) {
	// A direct probe: feed the parser a pathological input that would
	// cause parseIndexExpr to receive a nil left. The expected outcome
	// is a clean transpile error (or successful transpile if Go's
	// parser handles it), but *never* a panic. We can't easily craft
	// an input that triggers nil-left in isolation without going
	// through a real-world failure pattern, so the
	// MapOfArray/MapOfMap/ArrayValue tests above act as the practical
	// probes. This test is here as a placeholder for future targeted
	// probes; we just assert that the previous test inputs did not
	// crash the binary (Go's test runner would already have failed
	// the whole suite if they had).
	src := `package x

func f() {
	var _ = make(map[int][2]int)
}
`
	// Should not panic.
	_, _ = PureASTTranspile([]byte(src), "")
}
