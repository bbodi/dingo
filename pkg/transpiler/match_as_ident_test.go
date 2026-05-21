package transpiler

import (
	"strings"
	"testing"
)

// match_as_ident_test.go — regression tests for the expression finder
// treating every `match` token as a match-expression keyword, even
// when it appears in identifier position (method call, function name,
// method declaration).
//
// Background: Dingo's tokenizer maps the word `match` to a hard
// keyword token (MATCH). The expression finder scans for MATCH and
// tries to parse a `match scrutinee { arms }` expression at every
// occurrence. Real Go code that names a function or method `match`
// (the Go compiler's `cmd/compile/internal/base.HashDebug.match` and
// many similar) then trips the finder, which fails with
// `expected next token to be {` or similar downstream parse errors.
//
// The fix is contextual: skip MATCH when it sits in a position that
// can only be an identifier — preceded by `.` (selector), `func`
// (declaration), or `)` (method-of-receiver declaration).

func TestMatchIdent_MethodCall(t *testing.T) {
	src := `package x

type D struct{}
func (d *D) match(h int) *int { return nil }

func (d *D) f(hash int) bool {
	if m := d.match(hash); m != nil {
		return true
	}
	return false
}
`
	out, err := PureASTTranspile([]byte(src), "")
	if err != nil {
		t.Fatalf("transpile failed: %v", err)
	}
	if !strings.Contains(string(out), "d.match(hash)") {
		t.Errorf("method call corrupted.\n%s", out)
	}
}

func TestMatchIdent_FuncDecl(t *testing.T) {
	// Standalone `func match(...)` — the `match` is a function name.
	src := `package x

func match(h int) *int { return nil }

func use() {
	_ = match(7)
}
`
	out, err := PureASTTranspile([]byte(src), "")
	if err != nil {
		t.Fatalf("transpile failed: %v", err)
	}
	if !strings.Contains(string(out), "func match(") {
		t.Errorf("function decl named `match` corrupted.\n%s", out)
	}
	if !strings.Contains(string(out), "match(7)") {
		t.Errorf("call to `match` corrupted.\n%s", out)
	}
}

func TestMatchIdent_MethodDecl(t *testing.T) {
	// `func (d *D) match(...)` — `match` is a method name following the
	// receiver list's closing `)`.
	src := `package x

type D struct{}
func (d *D) match(h int) bool { return h > 0 }

func use(d *D) { _ = d.match(1) }
`
	out, err := PureASTTranspile([]byte(src), "")
	if err != nil {
		t.Fatalf("transpile failed: %v", err)
	}
	if !strings.Contains(string(out), ") match(") {
		t.Errorf("method decl named `match` corrupted.\n%s", out)
	}
}

func TestMatchIdent_VariableUsage(t *testing.T) {
	// `match` as an ordinary local variable. The Go compiler does this
	// in cmd/compile/internal/ssa/cpufeatures.dingo: a string `match`
	// is set inside a loop and then used as a switch subject.
	src := `package x

func f(items []string, want string) string {
	var match string
	for _, s := range items {
		if s == want {
			match = s
			break
		}
	}
	switch match {
	case "":
		return "miss"
	default:
		return match
	}
}
`
	out, err := PureASTTranspile([]byte(src), "")
	if err != nil {
		t.Fatalf("transpile failed: %v", err)
	}
	if !strings.Contains(string(out), "switch match {") {
		t.Errorf("switch on variable `match` corrupted.\n%s", out)
	}
	if !strings.Contains(string(out), "match = s") {
		t.Errorf("assignment to `match` corrupted.\n%s", out)
	}
}

func TestMatchIdent_BoolInIf(t *testing.T) {
	// `match` as a boolean local — `if match { ... }`.
	src := `package x

func f(b bool) int {
	match := b
	if match {
		return 1
	}
	return 0
}
`
	out, err := PureASTTranspile([]byte(src), "")
	if err != nil {
		t.Fatalf("transpile failed: %v", err)
	}
	if !strings.Contains(string(out), "if match {") {
		t.Errorf("if on variable `match` corrupted.\n%s", out)
	}
}

func TestMatchIdent_LocalLambdaCall(t *testing.T) {
	// `match` as a local variable bound to a lambda, called with
	// positional args. The Go compiler's ssa/_gen/rulegen.go does
	// exactly this: `match := func(x opData, strict bool, archname
	// string) bool { ... }` followed by `match(x, true, "generic")`.
	//
	// Before the fix, the tuple-literal pre-pass would treat
	// `(x, true, "generic")` as a 3-tuple because the preceding
	// token kind was MATCH rather than IDENT, producing the broken
	// output `match__tuple3__(x, true, "generic")`.
	src := `package x

func use() bool {
	match := func(a int, b bool, c string) bool { return b }
	return match(1, true, "hi")
}
`
	out, err := PureASTTranspile([]byte(src), "")
	if err != nil {
		t.Fatalf("transpile failed: %v", err)
	}
	if !strings.Contains(string(out), `match(1, true, "hi")`) {
		t.Errorf("call to local match var corrupted.\n%s", out)
	}
	if strings.Contains(string(out), "match__tuple") {
		t.Errorf("tuple-literal pass clobbered the call.\n%s", out)
	}
}

func TestMatchExpr_StillWorks(t *testing.T) {
	// Regression guard: ordinary match expressions must still be
	// recognised. The context skip must not be too greedy.
	src := `package x

type Color interface{ isColor() }
type Red struct{}
func (Red) isColor() {}
type Blue struct{}
func (Blue) isColor() {}

func name(c Color) string {
	return match c {
		Red => "red",
		Blue => "blue",
	}
}
`
	out, err := PureASTTranspile([]byte(src), "")
	if err != nil {
		t.Fatalf("transpile failed: %v", err)
	}
	// A real match expression lowers to a Go type switch.
	if !strings.Contains(string(out), ".(type)") {
		t.Errorf("match expression no longer lowered to type switch.\n%s", out)
	}
}
