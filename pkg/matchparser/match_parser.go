package matchparser

import (
	"fmt"
	"strings"

	"github.com/MadAppGang/dingo/pkg/ast"
	"github.com/MadAppGang/dingo/pkg/tokenizer"
)

// MatchParser parses pattern matching expressions
type MatchParser struct {
	tok        *tokenizer.Tokenizer
	errors     []error
	matchIDGen int // Unique ID generator for matches
}

// NewMatchParser creates a parser from tokens
func NewMatchParser(tok *tokenizer.Tokenizer) *MatchParser {
	return &MatchParser{
		tok:        tok,
		errors:     nil,
		matchIDGen: 0,
	}
}

// ParseMatchExpr parses a complete match expression
// Grammar: 'match' scrutinee '{' arm* '}'
func (p *MatchParser) ParseMatchExpr() (*ast.MatchExpr, error) {
	matchTok, err := p.tok.Expect(tokenizer.MATCH)
	if err != nil {
		return nil, err
	}

	// Parse scrutinee expression (everything until '{')
	scrutinee, err := p.parseScrutinee()
	if err != nil {
		return nil, err
	}

	openBrace, err := p.tok.Expect(tokenizer.LBRACE)
	if err != nil {
		return nil, err
	}

	// Parse arms
	arms, comments, err := p.parseArms()
	if err != nil {
		return nil, err
	}

	closeBrace, err := p.tok.Expect(tokenizer.RBRACE)
	if err != nil {
		return nil, err
	}

	p.matchIDGen++

	return &ast.MatchExpr{
		Match:      matchTok.Pos,
		Scrutinee:  scrutinee,
		OpenBrace:  openBrace.Pos,
		Arms:       arms,
		CloseBrace: closeBrace.Pos,
		MatchID:    p.matchIDGen,
		Comments:   comments,
	}, nil
}

// parseScrutinee parses the expression being matched
func (p *MatchParser) parseScrutinee() (ast.Expr, error) {
	startPos := p.tok.Current().Pos
	var text []string

	// Collect tokens until we hit '{'
	depth := 0
	for {
		tok := p.tok.Current()

		if tok.Kind == tokenizer.EOF {
			return nil, fmt.Errorf("unexpected EOF in match scrutinee")
		}

		if tok.Kind == tokenizer.LBRACE && depth == 0 {
			break
		}

		// Track nested braces
		if tok.Kind == tokenizer.LBRACE {
			depth++
		} else if tok.Kind == tokenizer.RBRACE {
			depth--
		}

		// Skip newlines in scrutinee
		if tok.Kind != tokenizer.NEWLINE {
			text = append(text, tok.Lit)
		}
		p.tok.Advance()
	}

	return &ast.RawExpr{
		StartPos: startPos,
		EndPos:   p.tok.Current().Pos,
		Text:     strings.TrimSpace(strings.Join(text, " ")),
	}, nil
}

// parseArms parses all match arms until closing brace
func (p *MatchParser) parseArms() ([]*ast.MatchArm, []*ast.Comment, error) {
	var arms []*ast.MatchArm
	var comments []*ast.Comment

	for {
		// Skip newlines between arms
		for p.tok.Match(tokenizer.NEWLINE) {
		}

		// Check for closing brace
		if p.tok.Current().Kind == tokenizer.RBRACE {
			break
		}

		// Check for standalone comment
		if p.tok.Current().Kind == tokenizer.COMMENT {
			comments = append(comments, &ast.Comment{
				Pos:  p.tok.Current().Pos,
				Text: p.tok.Current().Lit,
				Kind: ast.LineComment,
			})
			p.tok.Advance()
			continue
		}

		arm, err := p.parseArm()
		if err != nil {
			return nil, nil, err
		}
		arms = append(arms, arm)
	}

	return arms, comments, nil
}

// parseArm parses a single match arm
// Grammar: pattern ('if' | 'where' guard)? '=>' body ','?
func (p *MatchParser) parseArm() (*ast.MatchArm, error) {
	patternStart := p.tok.Current().Pos

	// Parse pattern
	pattern, err := p.parsePattern()
	if err != nil {
		return nil, err
	}

	arm := &ast.MatchArm{
		Pattern:    pattern,
		PatternPos: patternStart,
	}

	// Optional guard: 'if' or 'where' expr
	if p.tok.Current().Kind == tokenizer.IF || p.tok.Current().Kind == tokenizer.WHERE {
		arm.GuardPos = p.tok.Current().Pos
		p.tok.Advance()

		guard, err := p.parseGuard()
		if err != nil {
			return nil, err
		}
		arm.Guard = guard
	}

	// Expect '=>'
	arrowTok, err := p.tok.Expect(tokenizer.ARROW)
	if err != nil {
		return nil, fmt.Errorf("match arm missing '=>' after pattern %s at line %d", pattern.String(), p.tok.Current().Line)
	}
	arm.Arrow = arrowTok.Pos

	// Parse body
	body, isBlock, err := p.parseBody()
	if err != nil {
		return nil, err
	}
	arm.Body = body
	arm.IsBlock = isBlock

	// Optional comma after body
	p.tok.Match(tokenizer.COMMA)

	// Check for trailing comment (CRITICAL: P0 bug fix)
	// Comments appear AFTER the comma in tokenization
	if p.tok.Current().Kind == tokenizer.COMMENT {
		arm.Comment = &ast.Comment{
			Pos:  p.tok.Current().Pos,
			Text: p.tok.Current().Lit,
			Kind: ast.LineComment,
		}
		p.tok.Advance()
	}

	return arm, nil
}

// parsePattern parses a pattern (recursive for nested patterns)
// Grammar:
//
//	pattern := '_' | literal | identifier | constructor | tuple
//	constructor := IDENT '(' pattern (',' pattern)* ')'
//	tuple := '(' pattern (',' pattern)* ')'
func (p *MatchParser) parsePattern() (ast.Pattern, error) {
	tok := p.tok.Current()

	switch tok.Kind {
	case tokenizer.UNDERSCORE:
		p.tok.Advance()
		return &ast.WildcardPattern{Pos_: tok.Pos}, nil

	case tokenizer.INT, tokenizer.FLOAT, tokenizer.STRING:
		p.tok.Advance()
		return &ast.LiteralPattern{
			ValuePos: tok.Pos,
			Value:    tok.Lit,
			Kind:     literalKindFromToken(tok.Kind),
		}, nil

	case tokenizer.AMPERSAND:
		// `&IDENT` — mutable (in-place reference) binding inside a
		// constructor pattern's params, e.g. BinaryExpr(&X, &Y) =>
		// assignments to X in the arm body mutate the underlying variant
		// data instead of writing to a local copy.
		p.tok.Advance()
		ident := p.tok.Current()
		if ident.Kind != tokenizer.IDENT {
			return nil, fmt.Errorf("expected identifier after `&` in pattern at line %d, got %s", ident.Line, ident.Kind)
		}
		p.tok.Advance()
		// `&IDENT(` or `&IDENT{` is invalid — references only bind to a
		// single field, not nested patterns.
		if p.tok.Current().Kind == tokenizer.LPAREN || p.tok.Current().Kind == tokenizer.LBRACE {
			return nil, fmt.Errorf("`&` may only precede a binding name in patterns at line %d", ident.Line)
		}
		// Uppercase first char → represented as a nullary ConstructorPattern
		// (matching how the Pratt parser's isNullaryConstructor heuristic
		// would treat the same identifier). Lowercase → VariablePattern.
		if isNullaryConstructor(ident.Lit) {
			return &ast.ConstructorPattern{
				NamePos: ident.Pos,
				Name:    ident.Lit,
				Params:  nil,
				ByRef:   true,
			}, nil
		}
		return &ast.VariablePattern{
			NamePos: ident.Pos,
			Name:    ident.Lit,
			ByRef:   true,
		}, nil

	case tokenizer.IDENT:
		// Could be: variable, constructor (with parens/braces), or enum variant
		ident := tok
		p.tok.Advance()

		// Check for constructor pattern: Ident(...) or Ident{...}
		if p.tok.Current().Kind == tokenizer.LPAREN {
			return p.parseConstructorPattern(ident)
		}
		if p.tok.Current().Kind == tokenizer.LBRACE {
			return p.parseStructPattern(ident)
		}

		// Check for known constructors without params
		if isNullaryConstructor(ident.Lit) {
			return &ast.ConstructorPattern{
				NamePos: ident.Pos,
				Name:    ident.Lit,
				Params:  nil,
			}, nil
		}

		// Variable binding pattern
		return &ast.VariablePattern{
			NamePos: ident.Pos,
			Name:    ident.Lit,
		}, nil

	case tokenizer.LPAREN:
		return p.parseTuplePattern()

	default:
		return nil, fmt.Errorf("unexpected token %s in pattern at line %d", tok.Kind, tok.Line)
	}
}

// parseConstructorPattern parses Ok(x), Err(e), Some(v), etc.
// Also handles nested: Ok(Some(x))
func (p *MatchParser) parseConstructorPattern(nameTok tokenizer.Token) (ast.Pattern, error) {
	lparen, err := p.tok.Expect(tokenizer.LPAREN)
	if err != nil {
		return nil, err
	}

	var params []ast.Pattern

	for p.tok.Current().Kind != tokenizer.RPAREN {
		// Skip newlines inside constructor
		for p.tok.Match(tokenizer.NEWLINE) {
		}

		if p.tok.Current().Kind == tokenizer.RPAREN {
			break
		}

		param, err := p.parsePattern() // RECURSIVE: enables nesting!
		if err != nil {
			return nil, err
		}
		params = append(params, param)

		// Skip newlines after pattern
		for p.tok.Match(tokenizer.NEWLINE) {
		}

		if !p.tok.Match(tokenizer.COMMA) {
			break
		}
	}

	rparen, err := p.tok.Expect(tokenizer.RPAREN)
	if err != nil {
		return nil, err
	}

	return &ast.ConstructorPattern{
		NamePos: nameTok.Pos,
		Name:    nameTok.Lit,
		LParen:  lparen.Pos,
		Params:  params,
		RParen:  rparen.Pos,
	}, nil
}

// parseStructPattern parses struct-like patterns: Color_RGB{r, g, b}
func (p *MatchParser) parseStructPattern(nameTok tokenizer.Token) (ast.Pattern, error) {
	lbrace, err := p.tok.Expect(tokenizer.LBRACE)
	if err != nil {
		return nil, err
	}

	var params []ast.Pattern

	for p.tok.Current().Kind != tokenizer.RBRACE {
		// Skip newlines inside struct
		for p.tok.Match(tokenizer.NEWLINE) {
		}

		if p.tok.Current().Kind == tokenizer.RBRACE {
			break
		}

		param, err := p.parsePattern() // RECURSIVE
		if err != nil {
			return nil, err
		}
		params = append(params, param)

		// Skip newlines after pattern
		for p.tok.Match(tokenizer.NEWLINE) {
		}

		if !p.tok.Match(tokenizer.COMMA) {
			break
		}
	}

	rbrace, err := p.tok.Expect(tokenizer.RBRACE)
	if err != nil {
		return nil, err
	}

	// Reuse ConstructorPattern for struct patterns
	return &ast.ConstructorPattern{
		NamePos: nameTok.Pos,
		Name:    nameTok.Lit,
		LParen:  lbrace.Pos, // Using LParen/RParen fields for brace positions
		Params:  params,
		RParen:  rbrace.Pos,
	}, nil
}

// parseTuplePattern parses (a, b) or (Ok(x), Err(e))
func (p *MatchParser) parseTuplePattern() (ast.Pattern, error) {
	lparen, err := p.tok.Expect(tokenizer.LPAREN)
	if err != nil {
		return nil, err
	}

	var elements []ast.Pattern

	for p.tok.Current().Kind != tokenizer.RPAREN {
		// Skip newlines inside tuple
		for p.tok.Match(tokenizer.NEWLINE) {
		}

		if p.tok.Current().Kind == tokenizer.RPAREN {
			break
		}

		elem, err := p.parsePattern() // RECURSIVE
		if err != nil {
			return nil, err
		}
		elements = append(elements, elem)

		// Skip newlines after pattern
		for p.tok.Match(tokenizer.NEWLINE) {
		}

		if !p.tok.Match(tokenizer.COMMA) {
			break
		}
	}

	rparen, err := p.tok.Expect(tokenizer.RPAREN)
	if err != nil {
		return nil, err
	}

	return &ast.TuplePattern{
		LParen:   lparen.Pos,
		Elements: elements,
		RParen:   rparen.Pos,
	}, nil
}

// parseGuard parses guard expression after 'if'/'where'
func (p *MatchParser) parseGuard() (ast.Expr, error) {
	startPos := p.tok.Current().Pos
	var text []string
	depth := 0

	// Collect until '=>' (tracking parenthesis/brace depth)
	for {
		tok := p.tok.Current()

		if tok.Kind == tokenizer.EOF {
			return nil, fmt.Errorf("unexpected EOF in guard expression")
		}

		// Check for end of guard (at depth 0)
		if tok.Kind == tokenizer.ARROW && depth == 0 {
			break
		}

		// Track depth for nested expressions
		if tok.Kind == tokenizer.LPAREN || tok.Kind == tokenizer.LBRACE {
			depth++
		} else if tok.Kind == tokenizer.RPAREN || tok.Kind == tokenizer.RBRACE {
			depth--
		}

		// Skip newlines in guard
		if tok.Kind != tokenizer.NEWLINE {
			text = append(text, tok.Lit)
		}
		p.tok.Advance()
	}

	return &ast.RawExpr{
		StartPos: startPos,
		EndPos:   p.tok.Current().Pos,
		Text:     strings.TrimSpace(strings.Join(text, " ")),
	}, nil
}

// parseBody parses arm body (expression or block)
func (p *MatchParser) parseBody() (ast.Expr, bool, error) {
	startPos := p.tok.Current().Pos

	// Check for block body
	if p.tok.Current().Kind == tokenizer.LBRACE {
		return p.parseBlockBody()
	}

	// Expression body - collect until comma, comment, or closing brace
	// CRITICAL: Allow newlines within expression bodies for multi-line calls
	var text []string
	depth := 0

	for {
		tok := p.tok.Current()

		if tok.Kind == tokenizer.EOF {
			break
		}

		// Handle nested braces/parens in body
		if tok.Kind == tokenizer.LBRACE || tok.Kind == tokenizer.LPAREN {
			depth++
		} else if tok.Kind == tokenizer.RBRACE || tok.Kind == tokenizer.RPAREN {
			if depth == 0 && tok.Kind == tokenizer.RBRACE {
				break // End of match
			}
			if depth > 0 {
				depth--
			}
		}

		// End of expression body at comma or comment (NOT newline)
		// Newlines are allowed for multi-line expressions like fmt.Sprintf(...)
		if depth == 0 && (tok.Kind == tokenizer.COMMA || tok.Kind == tokenizer.COMMENT) {
			break
		}

		// Skip newlines in expression (don't include in text)
		if tok.Kind == tokenizer.NEWLINE {
			p.tok.Advance()
			continue
		}

		text = append(text, tok.Lit)
		p.tok.Advance()
	}

	return &ast.RawExpr{
		StartPos: startPos,
		EndPos:   p.tok.Current().Pos,
		Text:     strings.TrimSpace(strings.Join(text, " ")),
	}, false, nil
}

// parseBlockBody parses { ... } body
func (p *MatchParser) parseBlockBody() (ast.Expr, bool, error) {
	startPos := p.tok.Current().Pos

	lbrace, err := p.tok.Expect(tokenizer.LBRACE)
	if err != nil {
		return nil, false, err
	}

	var text []string
	text = append(text, "{")
	depth := 1

	for depth > 0 {
		tok := p.tok.Current()

		if tok.Kind == tokenizer.EOF {
			return nil, false, fmt.Errorf("unterminated block body at line %d", lbrace.Line)
		}

		if tok.Kind == tokenizer.LBRACE {
			depth++
		} else if tok.Kind == tokenizer.RBRACE {
			depth--
		}

		if tok.Kind == tokenizer.NEWLINE {
			text = append(text, "\n")
		} else {
			text = append(text, tok.Lit)
		}
		p.tok.Advance()
	}

	return &ast.RawExpr{
		StartPos: startPos,
		EndPos:   p.tok.Current().Pos,
		Text:     strings.TrimSpace(strings.Join(text, " ")),
	}, true, nil
}

// Helper functions

func isNullaryConstructor(name string) bool {
	return name == "None" || strings.HasSuffix(name, "_None")
}

func literalKindFromToken(kind tokenizer.TokenKind) ast.LiteralKind {
	switch kind {
	case tokenizer.INT:
		return ast.IntLiteral
	case tokenizer.FLOAT:
		return ast.FloatLiteral
	case tokenizer.STRING:
		return ast.StringLiteral
	default:
		return ast.IntLiteral
	}
}
