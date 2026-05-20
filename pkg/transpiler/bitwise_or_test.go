package transpiler

import (
	"strings"
	"testing"
)

// bitwise_or_test.go — regression tests for parsing `|` as a bitwise-or
// operator (as opposed to the start of a Rust-style lambda).
//
// History: the Pratt parser registered `|` (PIPE token) only as a prefix
// parselet for lambdas. As an infix operator it was unregistered, which
// meant that a chain like `A|B|C` inside a function-call argument list
// would re-enter `parseRustLambda` at the second pipe and fail with
// `expected '|' after lambda parameters` — or, for some surrounding
// contexts, a nil-pointer panic deep in `parseIndexExpr`. Real-world hit:
// 13 files in cmd/compile, all of the form `os.OpenFile(p, FLAG_A|FLAG_B|FLAG_C, mode)`.
//
// The fix registers `|` as an infix binary operator at bitwise-or
// precedence (below logical-or, above logical-and — matching Go).
//
// Each test feeds a snippet through the full transpile pipeline and
// asserts that (a) no error is returned, and (b) the `|` chain survives
// verbatim into the generated Go code.

func TestBitwiseOr_TwoOperands_InCallArg(t *testing.T) {
	src := `package x

import "os"

func f() {
	_, _ = os.OpenFile("p", os.O_WRONLY|os.O_CREATE, 0644)
}
`
	out, err := PureASTTranspile([]byte(src), "")
	if err != nil {
		t.Fatalf("transpile failed: %v", err)
	}
	if !strings.Contains(string(out), "os.O_WRONLY|os.O_CREATE") {
		t.Errorf("bitwise-or chain lost in transpile.\n%s", out)
	}
}

func TestBitwiseOr_ThreeOperands_InCallArg(t *testing.T) {
	// The original failure mode: three operands inside a function-call
	// argument. Before the fix this produced "expected '|' after lambda
	// parameters".
	src := `package x

import "os"

func f() {
	_, _ = os.OpenFile("p", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
}
`
	out, err := PureASTTranspile([]byte(src), "")
	if err != nil {
		t.Fatalf("transpile failed: %v", err)
	}
	if !strings.Contains(string(out), "os.O_WRONLY|os.O_CREATE|os.O_TRUNC") {
		t.Errorf("3-operand bitwise-or chain lost.\n%s", out)
	}
}

func TestBitwiseOr_FourOperands_InCallArg(t *testing.T) {
	// Stress test: deeper chains shouldn't trip the parser either.
	src := `package x

func f(a, b, c, d int) int {
	return g(a|b|c|d)
}

func g(x int) int { return x }
`
	out, err := PureASTTranspile([]byte(src), "")
	if err != nil {
		t.Fatalf("transpile failed: %v", err)
	}
	// The bitwise-or chain can survive with or without surrounding spaces
	// depending on intermediate transforms; both are valid Go.
	got := string(out)
	if !strings.Contains(got, "a|b|c|d") && !strings.Contains(got, "a | b | c | d") {
		t.Errorf("4-operand bitwise-or chain lost.\n%s", got)
	}
}

func TestBitwiseOr_OutsideCall_StillWorks(t *testing.T) {
	// Bitwise-or outside a function-call argument must of course also
	// keep working. This was likely already fine (the Go parser handled
	// it after Dingo gave up scanning), but pin it.
	src := `package x

func f() int {
	mode := 1 | 2 | 4
	return mode
}
`
	out, err := PureASTTranspile([]byte(src), "")
	if err != nil {
		t.Fatalf("transpile failed: %v", err)
	}
	if !strings.Contains(string(out), "1 | 2 | 4") && !strings.Contains(string(out), "1|2|4") {
		t.Errorf("bitwise-or in assignment lost.\n%s", out)
	}
}

func TestBitwiseOr_DoesNotBreakLambda(t *testing.T) {
	// Regression: registering `|` as infix must not stop the prefix
	// parselet from firing when `|` starts an expression (e.g. a
	// function argument that is a lambda).
	src := `package x

func g(f func(int) int) int { return f(1) }

func main() {
	_ = g(|x| x + 1)
}
`
	out, err := PureASTTranspile([]byte(src), "")
	if err != nil {
		t.Fatalf("transpile failed: %v", err)
	}
	// The lambda must transpile to a Go func literal — the exact form
	// depends on Dingo's lambda codegen, but at minimum we shouldn't get
	// a parse error and the generated code must reference `x+1` or
	// `x + 1` inside a function body.
	if !strings.Contains(string(out), "x + 1") && !strings.Contains(string(out), "x+1") {
		t.Errorf("lambda body missing from output.\n%s", out)
	}
}
