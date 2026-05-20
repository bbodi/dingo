package transpiler

import (
	"strings"
	"testing"
)

// explicit_any_lambda_test.go — regression tests for the lambda
// return-type inferrer narrowing a *user-written* `any` to the
// concrete returned value's type.
//
// History: Dingo lambda codegen emits `func(...) any { return X }`
// when the source lambda has no explicit return type. Layer 5 of
// the type inferrer then narrows that placeholder `any` to the
// inferred type of `X` so callers see a useful signature.
//
// But the inferrer's predicate matched on the AST shape alone —
// `func(...) any { return X }` — not on whether the `any` came
// from dingo's codegen vs. the user's source. Real Go code that
// returns `any` on purpose (sync.Pool.New, runtime.GOOS-keyed maps,
// reflective utilities) got its return type silently narrowed,
// breaking type-checked contracts at use sites.
//
// The fix: only narrow when the funcLit appears in a position
// dingo lambdas land in — the RHS of an assignment / short-var
// decl / var GenDecl. Composite literal fields, function call
// arguments, and other typed contexts get the user's `any` left
// alone.

func TestExplicitAny_SyncPoolField(t *testing.T) {
	// The motivating real-world hit:
	// src/cmd/compile/internal/types/fmt.dingo
	src := `package x

import (
	"bytes"
	"sync"
)

var p = sync.Pool{
	New: func() any {
		return new(bytes.Buffer)
	},
}
`
	out, err := PureASTTranspile([]byte(src), "")
	if err != nil {
		t.Fatalf("transpile failed: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, "func() any {") {
		t.Errorf("user-written `func() any` was narrowed.\n%s", got)
	}
	if strings.Contains(got, "func() *bytes.Buffer {") {
		t.Errorf("user-written `any` was rewritten to concrete type.\n%s", got)
	}
}

func TestExplicitAny_CallArgument(t *testing.T) {
	// `func() any` passed as a function argument must also be
	// preserved — the callee's parameter type is the authority.
	src := `package x

func run(f func() any) any { return f() }

func main() {
	_ = run(func() any { return 42 })
}
`
	out, err := PureASTTranspile([]byte(src), "")
	if err != nil {
		t.Fatalf("transpile failed: %v", err)
	}
	if !strings.Contains(string(out), "func() any { return 42 }") {
		t.Errorf("call-arg `func() any` was narrowed.\n%s", out)
	}
}

func TestExplicitAny_SliceElement(t *testing.T) {
	// `func() any` as an element of a `[]func() any` slice literal.
	src := `package x

var fs = []func() any{
	func() any { return 1 },
	func() any { return "two" },
}
`
	out, err := PureASTTranspile([]byte(src), "")
	if err != nil {
		t.Fatalf("transpile failed: %v", err)
	}
	got := string(out)
	if strings.Contains(got, "func() int {") || strings.Contains(got, "func() string {") {
		t.Errorf("slice-element `func() any` was narrowed.\n%s", got)
	}
}

func TestExplicitAny_MapValue(t *testing.T) {
	// `func() any` as a value in a `map[K]func() any` literal.
	src := `package x

var handlers = map[string]func() any{
	"a": func() any { return 1 },
	"b": func() any { return "two" },
}
`
	out, err := PureASTTranspile([]byte(src), "")
	if err != nil {
		t.Fatalf("transpile failed: %v", err)
	}
	got := string(out)
	if strings.Contains(got, "func() int {") || strings.Contains(got, "func() string {") {
		t.Errorf("map-value `func() any` was narrowed.\n%s", got)
	}
}

func TestDingoLambda_AnyStillNarrowed(t *testing.T) {
	// Counter-test: a dingo lambda assigned to a variable SHOULD
	// still get its placeholder `any` narrowed — that's the whole
	// point of Layer 5 inference. We want to make sure the fix
	// doesn't over-correct.
	//
	// `add := |x: int, y: int| x + y` lowers to
	// `add := func(x int, y int) any { return x + y }`, which
	// should then be narrowed to `func(x int, y int) int { ... }`.
	src := `package x

func main() {
	add := |x: int, y: int| x + y
	_ = add(1, 2)
}
`
	out, err := PureASTTranspile([]byte(src), "")
	if err != nil {
		t.Fatalf("transpile failed: %v", err)
	}
	got := string(out)
	if strings.Contains(got, "func(x int, y int) any {") {
		t.Errorf("dingo lambda placeholder `any` was not narrowed to int.\n%s", got)
	}
	if !strings.Contains(got, "func(x int, y int) int {") {
		t.Errorf("dingo lambda not narrowed to int return.\n%s", got)
	}
}
