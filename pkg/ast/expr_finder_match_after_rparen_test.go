package ast

import (
	"testing"
)

// TestFindDingoExpressions_MatchAfterFunctionCall verifies that a match
// statement following an unrelated function call (whose `)` is the
// previous non-trivia token) is still detected as a match expression.
//
// Regression: the backward context-check used to treat any preceding
// RPAREN as a method-receiver close (`func (r T) match(...)`) and
// short-circuit MATCH to identifier. That false negative dropped every
// `f(...)\nmatch e { ... }` form on the floor, leaving the arms of the
// match to be picked up by the lambda detector instead — which then
// failed because match-arm patterns aren't valid TS-lambda parameters.
func TestFindDingoExpressions_MatchAfterFunctionCall(t *testing.T) {
	src := []byte(`package p

func walk(e *E_, edit func(int) int) {
	editNodes(e.init, edit)
	match e {
		Foo(x) => x,
		_ => 0,
	}
}
`)

	locs, err := FindDingoExpressions(src)
	if err != nil {
		t.Fatalf("FindDingoExpressions returned error: %v", err)
	}

	var matchLoc *ExprLocation
	for i, l := range locs {
		if l.Kind == ExprMatch {
			matchLoc = &locs[i]
			break
		}
	}
	if matchLoc == nil {
		t.Fatalf("no ExprMatch location detected; got %+v", locs)
	}

	// The detector must consume the full match expression — confirm the
	// arms aren't separately exposed as lambdas (which would later fail
	// to parse).
	for _, l := range locs {
		if l.Kind == ExprLambdaTS && l.Start >= matchLoc.Start && l.End <= matchLoc.End {
			t.Errorf("match arm at byte %d was misdetected as a TS lambda", l.Start)
		}
	}
}

// TestFindDingoExpressions_MatchAsMethodNameStillSkipped is a guard
// regression: real method declarations like `func (r T) match(...)`
// must still treat `match` as an identifier, not a keyword. The
// previous overly-eager backward check was correct for this case; the
// new check must keep that behaviour.
func TestFindDingoExpressions_MatchAsMethodNameStillSkipped(t *testing.T) {
	src := []byte(`package p

type T struct{}

func (r T) match(x int) int { return x }
`)

	locs, err := FindDingoExpressions(src)
	if err != nil {
		t.Fatalf("FindDingoExpressions returned error: %v", err)
	}
	for _, l := range locs {
		if l.Kind == ExprMatch {
			t.Errorf("`match` in method declaration was incorrectly classified as match expression at %d", l.Start)
		}
	}
}
