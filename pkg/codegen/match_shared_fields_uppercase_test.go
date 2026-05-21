package codegen

import (
	"strings"
	"testing"

	"github.com/MadAppGang/dingo/pkg/ast"
)

// TestMatchCodeGen_SharedFields_UppercaseBindings exercises the case
// where pattern bindings inside a constructor match are uppercase —
// the convention for exported Go struct fields, which is precisely how
// the shared-fields enum layout names per-variant payload fields.
//
// Regression: the Pratt parser's isNullaryConstructor classifies any
// uppercase identifier as a nullary constructor, so a pattern like
// `BinaryExpr(X, Y)` parses with Params=[ConstructorPattern{X},
// ConstructorPattern{Y}] instead of [VariablePattern{X},
// VariablePattern{Y}]. emitSharedFieldsBindings used to only honour
// VariablePattern, so the cast `d := scrut.data.(*ExprBinaryExprData)`
// was emitted but the bindings `X := d.X; Y := d.Y` were dropped —
// leaving the arm body referring to undefined X and Y.
func TestMatchCodeGen_SharedFields_UppercaseBindings(t *testing.T) {
	matchExpr := &ast.MatchExpr{
		IsExpr:    true,
		Scrutinee: &ast.RawExpr{Text: "e"},
		Arms: []*ast.MatchArm{
			{
				// Mimic what the Pratt parser produces for the source
				// text `BinaryExpr(X, Y)`: nullary inner ConstructorPatterns.
				Pattern: &ast.ConstructorPattern{
					Name: "BinaryExpr",
					Params: []ast.Pattern{
						&ast.ConstructorPattern{Name: "X"},
						&ast.ConstructorPattern{Name: "Y"},
					},
				},
				Body: &ast.RawExpr{Text: "X + Y"},
			},
			{
				Pattern: &ast.ConstructorPattern{Name: "NilExpr"},
				Body:    &ast.RawExpr{Text: "0"},
			},
		},
	}

	r := ast.NewEnumRegistry()
	r.RegisterSumTypeVariant("BinaryExpr", "Expr", true)
	r.RegisterSumTypeVariant("NilExpr", "Expr", true)
	r.RegisterSharedFieldsEnum("Expr")

	ctx := &GenContext{
		EnumRegistry: map[string]string{"BinaryExpr": "Expr", "NilExpr": "Expr"},
		ValueEnumReg: r,
	}
	gen := NewMatchCodeGen(matchExpr).(*MatchCodeGen)
	gen.Context = ctx
	out := string(gen.Generate().Output)

	mustContain := []string{
		"case ExprTagBinaryExpr:",
		// The cast must be emitted...
		".data.(*ExprBinaryExprData)",
		// ...AND the bindings must follow, accessing the (uppercase)
		// exported fields and binding them to local vars of the same
		// (uppercase) name.
		"X := ",
		".X\n",
		"Y := ",
		".Y\n",
		// The arm body must still reference the bindings.
		"return X + Y",
		"case ExprTagNilExpr:",
		"return 0",
	}
	for _, s := range mustContain {
		if !strings.Contains(out, s) {
			t.Errorf("generated code missing %q.\nFull output:\n%s", s, out)
		}
	}
}
