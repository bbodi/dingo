package codegen

import (
	"strings"
	"testing"

	"github.com/MadAppGang/dingo/pkg/ast"
)

// TestMatchCodeGen_PointerVariantCase exercises match codegen for a
// pointer-stored variant: the generated case clause must use `case *T:`
// so the type switch matches when the interface holds a pointer.
func TestMatchCodeGen_PointerVariantCase(t *testing.T) {
	// Construct a registry as if parsing `enum Tree { *Node { ... }, Leaf }`
	registry := ast.NewEnumRegistry()
	registry.RegisterSumTypeVariant("Node", "Tree", true)
	registry.RegisterSumTypeVariant("Leaf", "Tree", false)

	matchExpr := &ast.MatchExpr{
		IsExpr:    true,
		Scrutinee: &ast.RawExpr{Text: "t"},
		Arms: []*ast.MatchArm{
			{
				Pattern: &ast.ConstructorPattern{
					Name: "Node",
					Params: []ast.Pattern{
						&ast.VariablePattern{Name: "value"},
					},
				},
				Body: &ast.RawExpr{Text: "value"},
			},
			{
				Pattern: &ast.ConstructorPattern{Name: "Leaf"},
				Body:    &ast.RawExpr{Text: "0"},
			},
		},
	}

	ctx := &GenContext{
		EnumRegistry: map[string]string{"Node": "Tree", "Leaf": "Tree"},
		ValueEnumReg: registry,
	}
	gen := NewMatchCodeGen(matchExpr).(*MatchCodeGen)
	gen.Context = ctx
	result := gen.Generate()
	out := string(result.Output)

	if !strings.Contains(out, "case *TreeNode:") {
		t.Errorf("expected `case *TreeNode:` (pointer case) in output, got:\n%s", out)
	}
	if !strings.Contains(out, "case TreeLeaf:") {
		t.Errorf("expected `case TreeLeaf:` (value case) for unit variant, got:\n%s", out)
	}
	if strings.Contains(out, "case TreeNode:") && !strings.Contains(out, "case *TreeNode:") {
		t.Errorf("Node case should be pointer-typed, found bare value case in:\n%s", out)
	}
}
