package transpiler

import (
	"strings"
	"testing"
)

// iife_call_test.go — regression tests for transformTupleLiterals
// misinterpreting the parentheses of an immediately-invoked function
// expression as a tuple literal.
//
// `func(x int){ ... }(1)` is a perfectly normal Go construct. The
// tuple-literal pass guards `(` preceded by IDENT/RPAREN/FUNC/RBRACKET
// as "definitely a call site, skip", but it forgot RBRACE. The `}`
// of an IIFE body looks like a call site too — `}(args)` is *only*
// a function call in Go (there is no `{...}(...)` composite-literal-
// followed-by-paren-grouping construct). Without the guard the IIFE
// arguments got rewritten into `__tuple2__(...)`, and the surrounding
// `defer` then failed with "expression in defer must be function call"
// because the call expression had become a bare identifier-prefixed
// tuple.
//
// Hit by types2/stmt.dingo (defer setup) and types2/decl.dingo.

func TestIIFE_DeferOfFuncLit(t *testing.T) {
	// The exact pattern from cmd/compile/internal/types2/stmt.dingo.
	src := `package x

func f() {
	defer func(env int, indent int) {
		_ = env + indent
	}(1, 2)
}
`
	out, err := PureASTTranspile([]byte(src), "")
	if err != nil {
		t.Fatalf("transpile failed: %v", err)
	}
	if strings.Contains(string(out), "__tuple") {
		t.Errorf("IIFE args rewritten to a tuple literal.\n%s", out)
	}
	if !strings.Contains(string(out), "}(1, 2)") {
		t.Errorf("IIFE call site lost.\n%s", out)
	}
}

func TestIIFE_PlainImmediateInvoke(t *testing.T) {
	// Same shape outside a defer — should also be unaffected.
	src := `package x

var x = func(a, b int) int { return a + b }(3, 4)
`
	out, err := PureASTTranspile([]byte(src), "")
	if err != nil {
		t.Fatalf("transpile failed: %v", err)
	}
	if strings.Contains(string(out), "__tuple") {
		t.Errorf("IIFE args rewritten to a tuple literal.\n%s", out)
	}
}

func TestIIFE_SingleArg_NotATuple(t *testing.T) {
	// Single-arg IIFEs don't have a comma, so the tuple detector
	// would not have triggered on them before — pin the behaviour
	// so a future skip-list tweak doesn't regress this either.
	src := `package x

var x = func(a int) int { return a }(5)
`
	out, err := PureASTTranspile([]byte(src), "")
	if err != nil {
		t.Fatalf("transpile failed: %v", err)
	}
	if !strings.Contains(string(out), "}(5)") {
		t.Errorf("single-arg IIFE call lost.\n%s", out)
	}
}

func TestIIFE_GoStmt_FuncLit(t *testing.T) {
	// `go func(){...}()` is another idiom that hits the same `}(`
	// prev-token pattern.
	src := `package x

func f() {
	go func(a int, b int) {
		_ = a + b
	}(10, 20)
}
`
	out, err := PureASTTranspile([]byte(src), "")
	if err != nil {
		t.Fatalf("transpile failed: %v", err)
	}
	if strings.Contains(string(out), "__tuple") {
		t.Errorf("go-func IIFE args rewritten to a tuple literal.\n%s", out)
	}
}
