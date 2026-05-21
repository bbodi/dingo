package ast

import (
	"go/token"
	"strings"
)

// MatchExpr represents a Rust-style pattern match expression
// Examples:
//   - match result { Ok(x) => x, Err(e) => 0 }
//   - match status { Status_Pending => "waiting", _ => "other" }
type MatchExpr struct {
	Match      token.Pos   // Position of 'match' keyword
	Scrutinee  Expr        // Expression being matched (AST node, not string)
	OpenBrace  token.Pos   // Position of '{'
	Arms       []*MatchArm // Match arms (pointer for mutability)
	CloseBrace token.Pos   // Position of '}'
	IsExpr     bool        // true if used as expression (return/assign)
	MatchID    int         // Unique ID for temp variable naming
	Comments   []*Comment  // Preserved inline comments
}

// MatchArm represents one arm of a match expression
// Example: Ok(Some(x)) if x > 0 => x * 2,  // success case
type MatchArm struct {
	Pattern    Pattern   // Pattern to match (supports nesting)
	PatternPos token.Pos // Position of pattern start
	Guard      Expr      // Optional guard expression (AST node)
	GuardPos   token.Pos // Position of 'if'/'where' keyword
	Arrow      token.Pos // Position of '=>'
	Body       Expr      // Body expression or block (AST node)
	IsBlock    bool      // true if body is { ... }
	Comment    *Comment  // Optional trailing comment
	Comma      token.Pos // Position of trailing comma (if present)
}

// Comment represents a preserved inline comment
type Comment struct {
	Pos  token.Pos
	Text string
	Kind CommentKind // Line or Block
}

type CommentKind int

const (
	LineComment  CommentKind = iota // // comment
	BlockComment                    // /* comment */
)

// Note: Expr interface is now defined in ast.go
// match.go uses the common Dingo Expr interface

// RawExpr wraps a string expression (interim solution)
type RawExpr struct {
	StartPos token.Pos
	EndPos   token.Pos
	Text     string
}

func (e *RawExpr) Node()          {}
func (e *RawExpr) exprNode()      {}
func (e *RawExpr) Pos() token.Pos { return e.StartPos }
func (e *RawExpr) End() token.Pos { return e.EndPos }
func (e *RawExpr) String() string { return e.Text }

// Pattern is the interface for all pattern types
type Pattern interface {
	PatternNode()           // Marker method
	Pos() token.Pos         // Start position
	End() token.Pos         // End position
	String() string         // String representation
	HasBindings() bool      // Does pattern bind variables?
	GetBindings() []Binding // Extract all bindings (recursive for nested)
}

// Binding represents a variable binding in a pattern
type Binding struct {
	Name string
	Pos  token.Pos
	Path []int // Path to binding in nested structure (e.g., [0, 1] for first.second)
}

// ConstructorPattern represents: Ok(x), Err(e), Some(v), None, EnumName_Variant(x)
// CRITICAL: Supports nested patterns in Params.
//
// When ByRef is true, this nullary ConstructorPattern was written as `&X`
// inside another pattern's params and binds X to an in-place reference to
// the underlying variant field. See the VariablePattern.ByRef comment for
// the mutation semantics.
type ConstructorPattern struct {
	NamePos token.Pos // Position of constructor name
	Name    string    // Constructor name (Ok, Err, Some, None, or qualified)
	LParen  token.Pos // Position of '(' (zero if no params)
	Params  []Pattern // CHANGED: Pattern instead of string (enables nesting!)
	RParen  token.Pos // Position of ')' (zero if no params)
	ByRef   bool      // True when written as `&X` and producing an in-place reference binding
}

func (p *ConstructorPattern) PatternNode() {}
func (p *ConstructorPattern) Pos() token.Pos {
	return p.NamePos
}
func (p *ConstructorPattern) End() token.Pos {
	if p.RParen.IsValid() {
		return p.RParen + 1
	}
	return p.NamePos + token.Pos(len(p.Name))
}
func (p *ConstructorPattern) String() string {
	if len(p.Params) == 0 {
		return p.Name
	}
	parts := make([]string, len(p.Params))
	for i, param := range p.Params {
		parts[i] = param.String()
	}
	return p.Name + "(" + strings.Join(parts, ", ") + ")"
}
func (p *ConstructorPattern) HasBindings() bool {
	for _, param := range p.Params {
		if param.HasBindings() {
			return true
		}
	}
	return false
}
func (p *ConstructorPattern) GetBindings() []Binding {
	var bindings []Binding
	for i, param := range p.Params {
		for _, b := range param.GetBindings() {
			// Prepend index to path
			b.Path = append([]int{i}, b.Path...)
			bindings = append(bindings, b)
		}
	}
	return bindings
}

// TuplePattern represents: (a, b), (Ok(x), Err(e))
// Supports nested patterns
type TuplePattern struct {
	LParen   token.Pos // Position of '('
	Elements []Pattern // Patterns for each element (supports nesting)
	RParen   token.Pos // Position of ')'
}

func (p *TuplePattern) PatternNode() {}
func (p *TuplePattern) Pos() token.Pos {
	return p.LParen
}
func (p *TuplePattern) End() token.Pos {
	return p.RParen + 1
}
func (p *TuplePattern) String() string {
	parts := make([]string, len(p.Elements))
	for i, elem := range p.Elements {
		parts[i] = elem.String()
	}
	return "(" + strings.Join(parts, ", ") + ")"
}
func (p *TuplePattern) HasBindings() bool {
	for _, elem := range p.Elements {
		if elem.HasBindings() {
			return true
		}
	}
	return false
}
func (p *TuplePattern) GetBindings() []Binding {
	var bindings []Binding
	for i, elem := range p.Elements {
		for _, b := range elem.GetBindings() {
			b.Path = append([]int{i}, b.Path...)
			bindings = append(bindings, b)
		}
	}
	return bindings
}

// VariablePattern represents: x, value (binding patterns).
//
// When ByRef is true, the binding was written with a leading `&` (e.g.
// `Variant(&X, &Y)`). Shared-fields enum match codegen emits these as
// in-place field references: assignments to X in the arm body mutate the
// underlying variant data instead of writing to a local copy. By-value
// bindings (ByRef=false) remain the default and produce a `X := d.X`
// local copy as before.
type VariablePattern struct {
	NamePos token.Pos
	Name    string
	ByRef   bool
}

func (p *VariablePattern) PatternNode() {}
func (p *VariablePattern) Pos() token.Pos {
	return p.NamePos
}
func (p *VariablePattern) End() token.Pos {
	return p.NamePos + token.Pos(len(p.Name))
}
func (p *VariablePattern) String() string {
	return p.Name
}
func (p *VariablePattern) HasBindings() bool {
	return true
}
func (p *VariablePattern) GetBindings() []Binding {
	return []Binding{{Name: p.Name, Pos: p.NamePos, Path: nil}}
}

// WildcardPattern represents: _
type WildcardPattern struct {
	Pos_ token.Pos
}

func (p *WildcardPattern) PatternNode() {}
func (p *WildcardPattern) Pos() token.Pos {
	return p.Pos_
}
func (p *WildcardPattern) End() token.Pos {
	return p.Pos_ + 1
}
func (p *WildcardPattern) String() string {
	return "_"
}
func (p *WildcardPattern) HasBindings() bool {
	return false
}
func (p *WildcardPattern) GetBindings() []Binding {
	return nil
}

// LiteralPattern represents: 1, "hello", true, 3.14
type LiteralPattern struct {
	ValuePos token.Pos
	Value    string
	Kind     LiteralKind
}

type LiteralKind int

const (
	IntLiteral LiteralKind = iota
	FloatLiteral
	StringLiteral
	BoolLiteral
)

func (p *LiteralPattern) PatternNode() {}
func (p *LiteralPattern) Pos() token.Pos {
	return p.ValuePos
}
func (p *LiteralPattern) End() token.Pos {
	return p.ValuePos + token.Pos(len(p.Value))
}
func (p *LiteralPattern) String() string {
	return p.Value
}
func (p *LiteralPattern) HasBindings() bool {
	return false
}
func (p *LiteralPattern) GetBindings() []Binding {
	return nil
}

// Node implements DingoNode marker interface
func (m *MatchExpr) Node() {}

// exprNode implements Expr interface
func (m *MatchExpr) exprNode() {}

// Pos returns the position of the match expression
func (m *MatchExpr) Pos() token.Pos {
	return m.Match
}

// End returns the end position
func (m *MatchExpr) End() token.Pos {
	if m.CloseBrace.IsValid() {
		return m.CloseBrace + 1
	}
	return m.Match
}

// String returns string representation for codegen
func (m *MatchExpr) String() string {
	var b strings.Builder
	b.WriteString("match ")
	b.WriteString(m.Scrutinee.String())
	b.WriteString(" { ")
	for i, arm := range m.Arms {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(arm.Pattern.String())
		if arm.Guard != nil {
			b.WriteString(" if ")
			b.WriteString(arm.Guard.String())
		}
		b.WriteString(" => ")
		b.WriteString(arm.Body.String())
	}
	b.WriteString(" }")
	return b.String()
}
