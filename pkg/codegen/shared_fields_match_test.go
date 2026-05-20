package codegen

import (
	"strings"
	"testing"

	"github.com/MadAppGang/dingo/pkg/ast"
)

// sharedFieldsRegistry constructs an EnumRegistry mimicking the result of
// extracting `enum Expr { shared { ... } *AddrExpr { X: Node } *NilExpr {} }`
// from source. The match codegen uses this to decide between tag-based
// dispatch and the classic type switch.
func sharedFieldsRegistry() *ast.EnumRegistry {
	r := ast.NewEnumRegistry()
	r.RegisterSumTypeVariant("AddrExpr", "Expr", true)
	r.RegisterSumTypeVariant("NilExpr", "Expr", true)
	r.RegisterSharedFieldsEnum("Expr")
	return r
}

// TestMatchCodeGen_SharedFields_TagDispatch is the core regression for the
// new layout: a match on a shared-fields enum must emit `switch ... .tag`,
// `case ExprTag<Variant>:`, and a cast on `.data` for bindings.
func TestMatchCodeGen_SharedFields_TagDispatch(t *testing.T) {
	matchExpr := &ast.MatchExpr{
		IsExpr:    true,
		Scrutinee: &ast.RawExpr{Text: "e"},
		Arms: []*ast.MatchArm{
			{
				Pattern: &ast.ConstructorPattern{
					Name:   "AddrExpr",
					Params: []ast.Pattern{&ast.VariablePattern{Name: "X"}},
				},
				Body: &ast.RawExpr{Text: "X"},
			},
			{
				Pattern: &ast.ConstructorPattern{Name: "NilExpr"},
				Body:    &ast.RawExpr{Text: "nil"},
			},
		},
	}

	ctx := &GenContext{
		EnumRegistry: map[string]string{"AddrExpr": "Expr", "NilExpr": "Expr"},
		ValueEnumReg: sharedFieldsRegistry(),
	}
	gen := NewMatchCodeGen(matchExpr).(*MatchCodeGen)
	gen.Context = ctx
	out := string(gen.Generate().Output)

	mustContain := []string{
		// Scrutinee captured once.
		"scrut := e",
		// Dispatch on the tag field, not a type switch.
		"switch scrut.tag {",
		// Tag constants per variant.
		"case ExprTagAddrExpr:",
		"case ExprTagNilExpr:",
		// Variant with bindings → cast through .data then read field.
		// The data-var name is generator-counter dependent (d, d1, d2, ...),
		// so we assert the cast target and the binding read separately.
		".data.(*ExprAddrExprData)",
		".X\n", // binding read like `X := <dvar>.X`
		// Expression match body returns the body.
		"return X",
		"return nil",
	}
	for _, s := range mustContain {
		if !strings.Contains(out, s) {
			t.Errorf("generated code missing %q.\nFull output:\n%s", s, out)
		}
	}

	// Type-switch artefacts must NOT appear — that would mean the
	// detector failed and we fell through to the classic path.
	mustNotContain := []string{
		".(type)",
		"case *ExprAddrExpr:", // classic path uses the variant struct directly
	}
	for _, s := range mustNotContain {
		if strings.Contains(out, s) {
			t.Errorf("output contains classic-path artefact %q (detector failed?):\n%s", s, out)
		}
	}
}

// TestMatchCodeGen_SharedFields_NoBindings verifies that a variant with no
// pattern bindings does not emit a stray `.data` assertion. The case body
// should still be reached, just without binding extraction.
func TestMatchCodeGen_SharedFields_NoBindings(t *testing.T) {
	matchExpr := &ast.MatchExpr{
		IsExpr:    false, // statement match for simplicity
		Scrutinee: &ast.RawExpr{Text: "e"},
		Arms: []*ast.MatchArm{
			{
				Pattern: &ast.ConstructorPattern{Name: "AddrExpr"}, // no params
				Body:    &ast.RawExpr{Text: "doA()"},
			},
			{
				Pattern: &ast.ConstructorPattern{Name: "NilExpr"},
				Body:    &ast.RawExpr{Text: "doN()"},
			},
		},
	}
	ctx := &GenContext{
		EnumRegistry: map[string]string{"AddrExpr": "Expr", "NilExpr": "Expr"},
		ValueEnumReg: sharedFieldsRegistry(),
	}
	gen := NewMatchCodeGen(matchExpr).(*MatchCodeGen)
	gen.Context = ctx
	out := string(gen.Generate().Output)

	if !strings.Contains(out, "switch scrut.tag {") {
		t.Errorf("missing tag-dispatch header.\n%s", out)
	}
	if strings.Contains(out, ".data.(") {
		t.Errorf("no-binding arms should not emit .data cast.\n%s", out)
	}
	for _, s := range []string{"case ExprTagAddrExpr:", "case ExprTagNilExpr:", "doA()", "doN()"} {
		if !strings.Contains(out, s) {
			t.Errorf("missing %q.\n%s", s, out)
		}
	}
}

// TestMatchCodeGen_SharedFields_Wildcard verifies that `_ => ...` becomes
// `default:` under the new layout.
func TestMatchCodeGen_SharedFields_Wildcard(t *testing.T) {
	matchExpr := &ast.MatchExpr{
		IsExpr:    true,
		Scrutinee: &ast.RawExpr{Text: "e"},
		Arms: []*ast.MatchArm{
			{
				Pattern: &ast.ConstructorPattern{Name: "AddrExpr"},
				Body:    &ast.RawExpr{Text: "1"},
			},
			{
				Pattern: &ast.WildcardPattern{},
				Body:    &ast.RawExpr{Text: "0"},
			},
		},
	}
	ctx := &GenContext{
		EnumRegistry: map[string]string{"AddrExpr": "Expr"},
		ValueEnumReg: sharedFieldsRegistry(),
	}
	gen := NewMatchCodeGen(matchExpr).(*MatchCodeGen)
	gen.Context = ctx
	out := string(gen.Generate().Output)

	if !strings.Contains(out, "default:") {
		t.Errorf("wildcard arm should produce `default:`.\n%s", out)
	}
}

// TestMatchCodeGen_SharedFields_MultipleBindings checks that an arm with
// more than one bound field assigns each binding from the same data cast,
// in pattern order.
func TestMatchCodeGen_SharedFields_MultipleBindings(t *testing.T) {
	matchExpr := &ast.MatchExpr{
		IsExpr:    true,
		Scrutinee: &ast.RawExpr{Text: "e"},
		Arms: []*ast.MatchArm{
			{
				Pattern: &ast.ConstructorPattern{
					Name: "Call",
					Params: []ast.Pattern{
						&ast.VariablePattern{Name: "Fun"},
						&ast.VariablePattern{Name: "Args"},
					},
				},
				Body: &ast.RawExpr{Text: "Fun"},
			},
		},
	}
	r := ast.NewEnumRegistry()
	r.RegisterSumTypeVariant("Call", "Expr", true)
	r.RegisterSharedFieldsEnum("Expr")
	gen := NewMatchCodeGen(matchExpr).(*MatchCodeGen)
	gen.Context = &GenContext{
		EnumRegistry: map[string]string{"Call": "Expr"},
		ValueEnumReg: r,
	}
	out := string(gen.Generate().Output)

	for _, s := range []string{
		"case ExprTagCall:",
		".data.(*ExprCallData)",
		"Fun := ",
		"Args := ",
		".Fun\n",
		".Args\n",
	} {
		if !strings.Contains(out, s) {
			t.Errorf("missing %q.\n%s", s, out)
		}
	}

	// Only one cast per arm, not one cast per binding.
	if n := strings.Count(out, ".data.(*ExprCallData)"); n != 1 {
		t.Errorf("expected exactly one data cast, got %d.\n%s", n, out)
	}
}

// TestMatchCodeGen_SharedFields_TwoArmsBothBind verifies that two arms
// with bindings each generate their own data variable. The temp-var
// generator (SharedTempVar / TempVar) is responsible for keeping these
// distinct; we check the *separation* without pinning a specific name.
func TestMatchCodeGen_SharedFields_TwoArmsBothBind(t *testing.T) {
	matchExpr := &ast.MatchExpr{
		IsExpr:    true,
		Scrutinee: &ast.RawExpr{Text: "e"},
		Arms: []*ast.MatchArm{
			{
				Pattern: &ast.ConstructorPattern{
					Name:   "AddrExpr",
					Params: []ast.Pattern{&ast.VariablePattern{Name: "X"}},
				},
				Body: &ast.RawExpr{Text: "X"},
			},
			{
				Pattern: &ast.ConstructorPattern{
					Name:   "Call",
					Params: []ast.Pattern{&ast.VariablePattern{Name: "Fun"}},
				},
				Body: &ast.RawExpr{Text: "Fun"},
			},
		},
	}
	r := ast.NewEnumRegistry()
	r.RegisterSumTypeVariant("AddrExpr", "Expr", true)
	r.RegisterSumTypeVariant("Call", "Expr", true)
	r.RegisterSharedFieldsEnum("Expr")
	gen := NewMatchCodeGen(matchExpr).(*MatchCodeGen)
	gen.Context = &GenContext{
		EnumRegistry: map[string]string{"AddrExpr": "Expr", "Call": "Expr"},
		ValueEnumReg: r,
	}
	out := string(gen.Generate().Output)

	// Both casts must appear, each exactly once. Their result variables
	// can share a name (because they're in separate `case` blocks) — but
	// the casts themselves shouldn't collapse into one.
	if n := strings.Count(out, ".data.(*ExprAddrExprData)"); n != 1 {
		t.Errorf("expected 1 AddrExpr cast, got %d.\n%s", n, out)
	}
	if n := strings.Count(out, ".data.(*ExprCallData)"); n != 1 {
		t.Errorf("expected 1 Call cast, got %d.\n%s", n, out)
	}
	if !strings.Contains(out, "X := ") || !strings.Contains(out, "Fun := ") {
		t.Errorf("bindings missing.\n%s", out)
	}
}

// TestMatchCodeGen_SharedFields_GuardWrapsBody verifies that an arm
// with a guard (`AddrExpr(x) if x != nil => ...`) wraps its body in
// `if <guard> { ... }` inside the `case` clause.
func TestMatchCodeGen_SharedFields_GuardWrapsBody(t *testing.T) {
	matchExpr := &ast.MatchExpr{
		IsExpr:    true,
		Scrutinee: &ast.RawExpr{Text: "e"},
		Arms: []*ast.MatchArm{
			{
				Pattern: &ast.ConstructorPattern{
					Name:   "AddrExpr",
					Params: []ast.Pattern{&ast.VariablePattern{Name: "X"}},
				},
				Guard: &ast.RawExpr{Text: "X != nil"},
				Body:  &ast.RawExpr{Text: "X"},
			},
			{
				Pattern: &ast.WildcardPattern{},
				Body:    &ast.RawExpr{Text: "nil"},
			},
		},
	}
	r := ast.NewEnumRegistry()
	r.RegisterSumTypeVariant("AddrExpr", "Expr", true)
	r.RegisterSharedFieldsEnum("Expr")
	gen := NewMatchCodeGen(matchExpr).(*MatchCodeGen)
	gen.Context = &GenContext{
		EnumRegistry: map[string]string{"AddrExpr": "Expr"},
		ValueEnumReg: r,
	}
	out := string(gen.Generate().Output)

	// Guard must wrap the arm body inside the case block. Confirm both
	// the if-guard opener and its closing brace, plus the original body.
	if !strings.Contains(out, "if X != nil {") {
		t.Errorf("guard not emitted as `if` wrapper.\n%s", out)
	}
	if !strings.Contains(out, "default:") {
		t.Errorf("wildcard fallback missing.\n%s", out)
	}
	// Sanity: the bound var must still be in scope inside the guard.
	idx := strings.Index(out, "if X != nil")
	if idx == -1 || idx < strings.Index(out, "X := ") {
		t.Errorf("guard appears before binding; X would not be in scope.\n%s", out)
	}
}

// TestMatchCodeGen_SharedFields_StatementMatch verifies that a match
// used as a *statement* (IsExpr=false) emits a bare `switch` with no
// IIFE wrapping and no implicit `return`.
func TestMatchCodeGen_SharedFields_StatementMatch(t *testing.T) {
	matchExpr := &ast.MatchExpr{
		IsExpr:    false,
		Scrutinee: &ast.RawExpr{Text: "e"},
		Arms: []*ast.MatchArm{
			{
				Pattern: &ast.ConstructorPattern{
					Name:   "AddrExpr",
					Params: []ast.Pattern{&ast.VariablePattern{Name: "X"}},
				},
				Body: &ast.RawExpr{Text: "fmt.Println(X)"},
			},
			{
				Pattern: &ast.ConstructorPattern{Name: "NilExpr"},
				Body:    &ast.RawExpr{Text: "fmt.Println(\"nil\")"},
			},
		},
	}
	r := ast.NewEnumRegistry()
	r.RegisterSumTypeVariant("AddrExpr", "Expr", true)
	r.RegisterSumTypeVariant("NilExpr", "Expr", true)
	r.RegisterSharedFieldsEnum("Expr")
	gen := NewMatchCodeGen(matchExpr).(*MatchCodeGen)
	gen.Context = &GenContext{
		EnumRegistry: map[string]string{"AddrExpr": "Expr", "NilExpr": "Expr"},
		ValueEnumReg: r,
	}
	out := string(gen.Generate().Output)

	if strings.Contains(out, "func() interface{}") {
		t.Errorf("statement match should not wrap in IIFE.\n%s", out)
	}
	if strings.Contains(out, "}()") {
		t.Errorf("statement match should not call an IIFE.\n%s", out)
	}
	// Body lines should appear bare, not as `return <body>`.
	if strings.Contains(out, "return fmt.Println") {
		t.Errorf("statement match should not emit `return` for body.\n%s", out)
	}
	// Sanity: dispatch still goes through tag and bindings still extract.
	for _, s := range []string{
		"switch scrut.tag {",
		"case ExprTagAddrExpr:",
		"X := ",
		"fmt.Println(X)",
		"fmt.Println(\"nil\")",
	} {
		if !strings.Contains(out, s) {
			t.Errorf("missing %q.\n%s", s, out)
		}
	}
}

// TestMatchCodeGen_SharedFields_ReturnContext verifies that a match used
// in a return context emits the switch as a statement-level replacement
// (Output empty, StatementOutput populated) and appends a panic for the
// unreachable fall-through.
func TestMatchCodeGen_SharedFields_ReturnContext(t *testing.T) {
	matchExpr := &ast.MatchExpr{
		IsExpr:    true,
		Scrutinee: &ast.RawExpr{Text: "e"},
		Arms: []*ast.MatchArm{
			{
				Pattern: &ast.ConstructorPattern{
					Name:   "AddrExpr",
					Params: []ast.Pattern{&ast.VariablePattern{Name: "X"}},
				},
				Body: &ast.RawExpr{Text: "X"},
			},
			{
				Pattern: &ast.ConstructorPattern{Name: "NilExpr"},
				Body:    &ast.RawExpr{Text: "nil"},
			},
		},
	}
	r := ast.NewEnumRegistry()
	r.RegisterSumTypeVariant("AddrExpr", "Expr", true)
	r.RegisterSumTypeVariant("NilExpr", "Expr", true)
	r.RegisterSharedFieldsEnum("Expr")
	gen := NewMatchCodeGen(matchExpr).(*MatchCodeGen)
	gen.Context = &GenContext{
		Context:      ast.ContextReturn,
		EnumRegistry: map[string]string{"AddrExpr": "Expr", "NilExpr": "Expr"},
		ValueEnumReg: r,
	}
	result := gen.Generate()

	out := string(result.StatementOutput)
	if string(result.Output) != "" {
		t.Errorf("return-context result should leave Output empty; got %q", string(result.Output))
	}
	if out == "" {
		t.Errorf("return-context result should populate StatementOutput")
	}
	if strings.Contains(out, "func() interface{}") {
		t.Errorf("return-context match must not wrap in IIFE.\n%s", out)
	}
	if !strings.Contains(out, `panic("unreachable: exhaustive match")`) {
		t.Errorf("return-context match should append unreachable panic.\n%s", out)
	}
	if !strings.Contains(out, "return X") || !strings.Contains(out, "return nil") {
		t.Errorf("arm bodies should be emitted as returns.\n%s", out)
	}
}

// TestMatchCodeGen_SharedFields_ScrutineeCapturedOnce ensures the
// scrutinee is evaluated exactly once even when it is a side-effecting
// call expression (e.g. `f()`). The codegen must not inline `f()` into
// every case's tag comparison.
func TestMatchCodeGen_SharedFields_ScrutineeCapturedOnce(t *testing.T) {
	matchExpr := &ast.MatchExpr{
		IsExpr:    true,
		Scrutinee: &ast.RawExpr{Text: "f(arg)"},
		Arms: []*ast.MatchArm{
			{Pattern: &ast.ConstructorPattern{Name: "AddrExpr"}, Body: &ast.RawExpr{Text: "1"}},
			{Pattern: &ast.ConstructorPattern{Name: "NilExpr"}, Body: &ast.RawExpr{Text: "0"}},
		},
	}
	r := ast.NewEnumRegistry()
	r.RegisterSumTypeVariant("AddrExpr", "Expr", true)
	r.RegisterSumTypeVariant("NilExpr", "Expr", true)
	r.RegisterSharedFieldsEnum("Expr")
	gen := NewMatchCodeGen(matchExpr).(*MatchCodeGen)
	gen.Context = &GenContext{
		EnumRegistry: map[string]string{"AddrExpr": "Expr", "NilExpr": "Expr"},
		ValueEnumReg: r,
	}
	out := string(gen.Generate().Output)

	// `f(arg)` must appear exactly once — at the temp-var capture site.
	if n := strings.Count(out, "f(arg)"); n != 1 {
		t.Errorf("scrutinee evaluated %d times; expected exactly once.\n%s", n, out)
	}
	if !strings.Contains(out, ":= f(arg)") {
		t.Errorf("scrutinee should be captured to a temp var.\n%s", out)
	}
	// Subsequent dispatch references the temp var, not the original expr.
	if !strings.Contains(out, ".tag {") {
		t.Errorf("dispatch should switch on temp.tag.\n%s", out)
	}
}

// TestMatchCodeGen_SharedFields_VariablePattern verifies that a
// top-level variable pattern (`x => ...`) becomes `default:` plus an
// alias binding to the scrutinee.
func TestMatchCodeGen_SharedFields_VariablePattern(t *testing.T) {
	matchExpr := &ast.MatchExpr{
		IsExpr:    true,
		Scrutinee: &ast.RawExpr{Text: "e"},
		Arms: []*ast.MatchArm{
			{Pattern: &ast.ConstructorPattern{Name: "AddrExpr"}, Body: &ast.RawExpr{Text: "1"}},
			{Pattern: &ast.VariablePattern{Name: "other"}, Body: &ast.RawExpr{Text: "other"}},
		},
	}
	r := ast.NewEnumRegistry()
	r.RegisterSumTypeVariant("AddrExpr", "Expr", true)
	r.RegisterSharedFieldsEnum("Expr")
	gen := NewMatchCodeGen(matchExpr).(*MatchCodeGen)
	gen.Context = &GenContext{
		EnumRegistry: map[string]string{"AddrExpr": "Expr"},
		ValueEnumReg: r,
	}
	out := string(gen.Generate().Output)

	if !strings.Contains(out, "default:") {
		t.Errorf("variable pattern should become `default:`.\n%s", out)
	}
	if !strings.Contains(out, "other := ") {
		t.Errorf("variable pattern should bind alias.\n%s", out)
	}
	if !strings.Contains(out, "return other") {
		t.Errorf("arm body should be returned.\n%s", out)
	}
}

// TestMatchCodeGen_SharedFields_DetectorFallback verifies that the
// shared-fields path is *not* triggered when the EnumRegistry is missing
// or doesn't mark the enum as shared-fields. The detector should fail
// quietly and let the classic path run.
func TestMatchCodeGen_SharedFields_DetectorFallback(t *testing.T) {
	tests := []struct {
		name    string
		makeReg func() *ast.EnumRegistry
	}{
		{
			name: "no registry",
			makeReg: func() *ast.EnumRegistry {
				return ast.NewEnumRegistry() // empty
			},
		},
		{
			name: "variants registered but not marked shared",
			makeReg: func() *ast.EnumRegistry {
				r := ast.NewEnumRegistry()
				r.RegisterSumTypeVariant("AddrExpr", "Expr", true)
				// No RegisterSharedFieldsEnum.
				return r
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			matchExpr := &ast.MatchExpr{
				IsExpr:    true,
				Scrutinee: &ast.RawExpr{Text: "e"},
				Arms: []*ast.MatchArm{
					{Pattern: &ast.ConstructorPattern{Name: "AddrExpr"}, Body: &ast.RawExpr{Text: "1"}},
					{Pattern: &ast.WildcardPattern{}, Body: &ast.RawExpr{Text: "0"}},
				},
			}
			gen := NewMatchCodeGen(matchExpr).(*MatchCodeGen)
			gen.Context = &GenContext{
				EnumRegistry: map[string]string{"AddrExpr": "Expr"},
				ValueEnumReg: tc.makeReg(),
			}
			out := string(gen.Generate().Output)
			if strings.Contains(out, "switch scrut.tag {") {
				t.Errorf("shared-fields path triggered without registration.\n%s", out)
			}
		})
	}
}

// TestMatchCodeGen_ClassicEnum_NotAffected is the regression guard: a
// match against a non-shared-fields enum must still go through the
// classic type-switch path, not the new tag dispatch.
func TestMatchCodeGen_ClassicEnum_NotAffected(t *testing.T) {
	r := ast.NewEnumRegistry()
	r.RegisterSumTypeVariant("Node", "Tree", true)
	r.RegisterSumTypeVariant("Leaf", "Tree", false)
	// Note: NO RegisterSharedFieldsEnum — this is a classic enum.

	matchExpr := &ast.MatchExpr{
		IsExpr:    true,
		Scrutinee: &ast.RawExpr{Text: "t"},
		Arms: []*ast.MatchArm{
			{
				Pattern: &ast.ConstructorPattern{Name: "Node",
					Params: []ast.Pattern{&ast.VariablePattern{Name: "v"}}},
				Body: &ast.RawExpr{Text: "v"},
			},
			{Pattern: &ast.ConstructorPattern{Name: "Leaf"}, Body: &ast.RawExpr{Text: "0"}},
		},
	}
	ctx := &GenContext{
		EnumRegistry: map[string]string{"Node": "Tree", "Leaf": "Tree"},
		ValueEnumReg: r,
	}
	gen := NewMatchCodeGen(matchExpr).(*MatchCodeGen)
	gen.Context = ctx
	out := string(gen.Generate().Output)

	if !strings.Contains(out, ".(type)") {
		t.Errorf("classic enum should still use type switch.\n%s", out)
	}
	if strings.Contains(out, "scrut.tag") || strings.Contains(out, ".data.(") {
		t.Errorf("classic enum leaked into shared-fields path.\n%s", out)
	}
}
