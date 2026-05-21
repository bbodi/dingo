package parser

import (
	"testing"

	"github.com/MadAppGang/dingo/pkg/ast"
	"github.com/MadAppGang/dingo/pkg/tokenizer"
)

// TestParsePattern_ByRefBinding verifies that the Pratt parser turns a
// `&X`-prefixed name inside a constructor pattern into a binding with the
// ByRef flag set. Lowercase names land in VariablePattern; uppercase land
// in nullary ConstructorPattern (matching the existing convention for
// pattern-position identifiers).
func TestParsePattern_ByRefBinding(t *testing.T) {
	src := []byte(`match e {
		BinaryExpr(&X, &y) => X + y,
		_ => 0,
	}`)

	tok := tokenizer.New(src)
	if _, err := tok.Tokenize(); err != nil {
		t.Fatalf("tokenization failed: %v", err)
	}
	tok.Reset()

	parser := NewPrattParser(tok)
	expr := parser.ParseExpression(PrecLowest)
	if len(parser.errors) != 0 {
		t.Fatalf("parser errors: %+v", parser.errors)
	}

	matchExpr, ok := expr.(*ast.MatchExpr)
	if !ok {
		t.Fatalf("expected *ast.MatchExpr, got %T", expr)
	}
	if len(matchExpr.Arms) != 2 {
		t.Fatalf("expected 2 arms, got %d", len(matchExpr.Arms))
	}

	pat, ok := matchExpr.Arms[0].Pattern.(*ast.ConstructorPattern)
	if !ok {
		t.Fatalf("arm[0]: expected ConstructorPattern, got %T", matchExpr.Arms[0].Pattern)
	}
	if pat.Name != "BinaryExpr" {
		t.Errorf("arm[0]: constructor name = %q, want %q", pat.Name, "BinaryExpr")
	}
	if len(pat.Params) != 2 {
		t.Fatalf("arm[0]: expected 2 params, got %d", len(pat.Params))
	}

	// First param: &X — uppercase, so the Pratt parser produces a nullary
	// ConstructorPattern.
	p0, ok := pat.Params[0].(*ast.ConstructorPattern)
	if !ok {
		t.Fatalf("param[0]: expected ConstructorPattern, got %T", pat.Params[0])
	}
	if p0.Name != "X" {
		t.Errorf("param[0]: name = %q, want %q", p0.Name, "X")
	}
	if !p0.ByRef {
		t.Errorf("param[0]: ByRef = false, want true")
	}
	if len(p0.Params) != 0 {
		t.Errorf("param[0]: should be nullary, got %d params", len(p0.Params))
	}

	// Second param: &y — lowercase, so VariablePattern.
	p1, ok := pat.Params[1].(*ast.VariablePattern)
	if !ok {
		t.Fatalf("param[1]: expected VariablePattern, got %T", pat.Params[1])
	}
	if p1.Name != "y" {
		t.Errorf("param[1]: name = %q, want %q", p1.Name, "y")
	}
	if !p1.ByRef {
		t.Errorf("param[1]: ByRef = false, want true")
	}
}

// TestParsePattern_ByRefRejectsConstructor verifies that `&Foo(x)` (a `&`
// followed by a non-nullary pattern) is rejected at parse time — refs can
// only bind to a single field, not to a nested constructor.
func TestParsePattern_ByRefRejectsConstructor(t *testing.T) {
	src := []byte(`match e {
		Outer(&Inner(x)) => x,
	}`)

	tok := tokenizer.New(src)
	if _, err := tok.Tokenize(); err != nil {
		t.Fatalf("tokenization failed: %v", err)
	}
	tok.Reset()

	parser := NewPrattParser(tok)
	parser.ParseExpression(PrecLowest)
	if len(parser.errors) == 0 {
		t.Errorf("expected parse error for `&Inner(x)`, got none")
	}
}
