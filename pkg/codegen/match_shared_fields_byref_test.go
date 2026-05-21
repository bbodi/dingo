package codegen

import (
	"strings"
	"testing"

	"github.com/MadAppGang/dingo/pkg/ast"
)

// TestMatchCodeGen_SharedFields_ByRefBinding verifies that a `&X` binding in
// a constructor pattern emits no `X := d.X` copy line and instead
// substitutes the identifier X in the arm body with the in-place
// field-access expression `d.X`. That way an assignment to X in the body
// (e.g. `X = X * 2`) mutates the underlying variant data, not a local
// copy — matching the behaviour of a hand-written
// `switch e.tag { case Tag: d := ...; d.X = d.X * 2 }`.
func TestMatchCodeGen_SharedFields_ByRefBinding(t *testing.T) {
	matchExpr := &ast.MatchExpr{
		IsExpr:    false, // statement context — the body has assignments
		Scrutinee: &ast.RawExpr{Text: "e"},
		Arms: []*ast.MatchArm{
			{
				Pattern: &ast.ConstructorPattern{
					Name: "BinaryExpr",
					Params: []ast.Pattern{
						// Uppercase ⇒ ConstructorPattern by the Pratt
						// parser's heuristic; ByRef carries the `&`.
						&ast.ConstructorPattern{Name: "X", ByRef: true},
						&ast.ConstructorPattern{Name: "Y", ByRef: true},
					},
				},
				Body: &ast.RawExpr{Text: "{\n\tX = X * 2\n\tY = Y * 2\n}"},
			},
			{
				Pattern: &ast.WildcardPattern{},
				Body:    &ast.RawExpr{Text: "{}"},
			},
		},
	}

	r := ast.NewEnumRegistry()
	r.RegisterSumTypeVariant("BinaryExpr", "Expr", true)
	r.RegisterSharedFieldsEnum("Expr")
	ctx := &GenContext{
		EnumRegistry: map[string]string{"BinaryExpr": "Expr"},
		ValueEnumReg: r,
	}
	gen := NewMatchCodeGen(matchExpr).(*MatchCodeGen)
	gen.Context = ctx
	out := string(gen.Generate().Output)

	// `&X` and `&Y` must NOT produce a `X :=` / `Y :=` local copy line.
	if strings.Contains(out, "X := ") || strings.Contains(out, "Y := ") {
		t.Errorf("by-ref bindings should not emit `X := ...` copy lines; output:\n%s", out)
	}
	// The body must see the in-place field-access form.
	if !strings.Contains(out, "d1.X = d1.X * 2") {
		t.Errorf("body should substitute X → d1.X for in-place mutation; output:\n%s", out)
	}
	if !strings.Contains(out, "d1.Y = d1.Y * 2") {
		t.Errorf("body should substitute Y → d1.Y for in-place mutation; output:\n%s", out)
	}
}

// TestMatchCodeGen_SharedFields_ByValueStillCopies guards the default
// behaviour: a binding written WITHOUT `&` (the existing form, e.g.
// `BinaryExpr(X, Y)`) still emits the local-copy line. Mixing by-ref and
// by-value bindings in the same pattern must also work.
func TestMatchCodeGen_SharedFields_ByValueStillCopies(t *testing.T) {
	matchExpr := &ast.MatchExpr{
		IsExpr:    true,
		Scrutinee: &ast.RawExpr{Text: "e"},
		Arms: []*ast.MatchArm{
			{
				Pattern: &ast.ConstructorPattern{
					Name: "BinaryExpr",
					Params: []ast.Pattern{
						// First binding by-value (no `&`), second by-ref.
						&ast.ConstructorPattern{Name: "X"},
						&ast.ConstructorPattern{Name: "Y", ByRef: true},
					},
				},
				Body: &ast.RawExpr{Text: "X + Y"},
			},
			{
				Pattern: &ast.WildcardPattern{},
				Body:    &ast.RawExpr{Text: "0"},
			},
		},
	}
	r := ast.NewEnumRegistry()
	r.RegisterSumTypeVariant("BinaryExpr", "Expr", true)
	r.RegisterSharedFieldsEnum("Expr")
	ctx := &GenContext{
		EnumRegistry: map[string]string{"BinaryExpr": "Expr"},
		ValueEnumReg: r,
	}
	gen := NewMatchCodeGen(matchExpr).(*MatchCodeGen)
	gen.Context = ctx
	out := string(gen.Generate().Output)

	// X is by-value → copy line emitted.
	if !strings.Contains(out, "X := ") {
		t.Errorf("by-value binding X should produce a `X := ...` copy line; output:\n%s", out)
	}
	// Y is by-ref → no copy line, body sees `d1.Y`.
	if strings.Contains(out, "Y := ") {
		t.Errorf("by-ref binding Y should NOT produce a copy line; output:\n%s", out)
	}
	// Body uses X (the local copy) and d1.Y (the substituted ref).
	if !strings.Contains(out, "return X + d1.Y") {
		t.Errorf("body should read X (copy) and d1.Y (ref); output:\n%s", out)
	}
}
