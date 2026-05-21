package codegen

import (
	"strings"
	"testing"

	"github.com/MadAppGang/dingo/pkg/ast"
)

// TestMatchCodeGen_SharedFields_PanicInArm verifies that a panic(...) call
// as an arm body is emitted as a bare statement, not wrapped in `return`.
//
// Regression: the shared-fields match codegen used to emit
// `return panic("...")` for every expression-context arm — but in Go,
// `panic` returns no value and cannot legally appear after `return`. The
// compiler would reject the generated code with
//
//	cannot use panic(...) (no value) as <T> value in return statement
//
// Fix: detect `panic(...)` arm bodies and emit them without the `return`
// prefix; control never falls through anyway.
func TestMatchCodeGen_SharedFields_PanicInArm(t *testing.T) {
	matchExpr := &ast.MatchExpr{
		IsExpr:    true,
		Scrutinee: &ast.RawExpr{Text: "e"},
		Arms: []*ast.MatchArm{
			{
				Pattern: &ast.ConstructorPattern{
					Name:   "BasicLit",
					Params: []ast.Pattern{&ast.ConstructorPattern{Name: "Val"}},
				},
				Body: &ast.RawExpr{Text: "Val"},
			},
			{
				Pattern: &ast.WildcardPattern{},
				// Wildcard arm panics — this is exactly the
				// `Val()` / `SetVal()` shape in cmd/compile.
				Body: &ast.RawExpr{Text: `panic("not a BasicLit")`},
			},
		},
	}

	r := ast.NewEnumRegistry()
	r.RegisterSumTypeVariant("BasicLit", "Expr", true)
	r.RegisterSharedFieldsEnum("Expr")

	ctx := &GenContext{
		EnumRegistry: map[string]string{"BasicLit": "Expr"},
		ValueEnumReg: r,
	}
	gen := NewMatchCodeGen(matchExpr).(*MatchCodeGen)
	gen.Context = ctx
	out := string(gen.Generate().Output)

	if strings.Contains(out, "return panic(") {
		t.Errorf("generated `return panic(...)` (invalid Go); output:\n%s", out)
	}
	// The panic should still appear as a bare statement.
	if !strings.Contains(out, `panic("not a BasicLit")`) {
		t.Errorf("panic call missing from arm body; output:\n%s", out)
	}
	// Value arm should still use `return`.
	if !strings.Contains(out, "return Val") {
		t.Errorf("value arm should still emit `return Val`; output:\n%s", out)
	}
}
