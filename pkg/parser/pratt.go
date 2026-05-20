// Package parser provides Pratt parser implementation for expression parsing
package parser

import (
	"fmt"
	"go/token"

	"github.com/MadAppGang/dingo/pkg/ast"
	"github.com/MadAppGang/dingo/pkg/tokenizer"
)

// Precedence levels for operators (higher = tighter binding)
const (
	PrecLowest     = iota
	PrecTernary    // ? : (ternary)
	PrecNullCoal   // ?? (null coalescing)
	PrecLogicalOr  // ||
	PrecLogicalAnd // &&
	PrecEquality   // == !=
	PrecComparison // < > <= >=
	PrecAdditive   // + -
	PrecMultiply   // * / %
	PrecUnary      // ! - +
	PrecPostfix    // ? ?. (error prop, safe nav)
	PrecCall       // () [] .
)

// operatorPrecedence maps token types to their precedence levels
var operatorPrecedence = map[tokenizer.TokenKind]int{
	// Dingo operators
	tokenizer.QUESTION:          PrecTernary,  // ? : (ternary) - also handles x? (error prop) via disambiguation
	tokenizer.QUESTION_QUESTION: PrecNullCoal, // a ?? b (null coalescing)
	tokenizer.QUESTION_DOT:      PrecPostfix,  // x?.field (safe navigation)

	// Standard Go operators
	tokenizer.DOT:      PrecCall, // x.y (selector/method call)
	tokenizer.LPAREN:   PrecCall, // x() (function call)
	tokenizer.LBRACKET: PrecCall, // x[i] (index expression)

	// Binary operators
	tokenizer.OR:    PrecLogicalOr,  // ||
	tokenizer.AND:   PrecLogicalAnd, // &&
	tokenizer.EQ:    PrecEquality,   // ==
	tokenizer.NE:    PrecEquality,   // !=
	tokenizer.LT:    PrecComparison, // <
	tokenizer.GT:    PrecComparison, // >
	tokenizer.LE:    PrecComparison, // <=
	tokenizer.GE:    PrecComparison, // >=
	tokenizer.PLUS:  PrecAdditive,   // +
	tokenizer.MINUS: PrecAdditive,   // -
	tokenizer.STAR:  PrecMultiply,   // *
	tokenizer.SLASH: PrecMultiply,   // /

	// Bitwise-or. Go puts `|` at additive precedence (same as `+`,`-`).
	// `|` is ALSO the start of a Rust-style lambda (handled by the prefix
	// parselet); registering it as infix here only affects positions
	// after an LHS expression. The prefix parselet still wins when `|`
	// starts a sub-expression (e.g. as a top-level argument: f(|x| x+1)).
	tokenizer.PIPE: PrecAdditive, // | (bitwise or)
}

// PrattParser implements a Pratt parser for expressions
type PrattParser struct {
	tokenizer *tokenizer.Tokenizer
	errors    []ParseError

	// Current and peek tokens
	curToken  tokenizer.Token
	peekToken tokenizer.Token

	// Prefix and infix parse functions
	prefixParseFns map[tokenizer.TokenKind]prefixParseFn
	infixParseFns  map[tokenizer.TokenKind]infixParseFn

	// Callback for collecting tuple literals during parsing
	OnTupleLiteral func(*ast.TupleLiteral)

	// Callback for collecting Dingo expression nodes during parsing
	OnDingoNode func(ast.DingoNode)
}

// ParseError is an alias for SpanError for backward compatibility
// New code should use SpanError directly for full span information
type ParseError = SpanError

// Parse function types
type (
	prefixParseFn func() ast.Expr
	infixParseFn  func(ast.Expr) ast.Expr
)

// NewPrattParser creates a new Pratt parser
func NewPrattParser(t *tokenizer.Tokenizer) *PrattParser {
	p := &PrattParser{
		tokenizer:      t,
		errors:         make([]ParseError, 0, 8),                        // Pre-allocate for typical error count
		prefixParseFns: make(map[tokenizer.TokenKind]prefixParseFn, 16), // Pre-allocate for registered handlers
		infixParseFns:  make(map[tokenizer.TokenKind]infixParseFn, 16),  // Pre-allocate for registered handlers
	}

	// Register prefix parse functions
	p.registerPrefix(tokenizer.IDENT, p.parseIdentifier)
	p.registerPrefix(tokenizer.INT, p.parseIntegerLiteral)
	p.registerPrefix(tokenizer.FLOAT, p.parseFloatLiteral)
	p.registerPrefix(tokenizer.STRING, p.parseStringLiteral)
	p.registerPrefix(tokenizer.TRUE, p.parseBoolLiteral)
	p.registerPrefix(tokenizer.FALSE, p.parseBoolLiteral)
	p.registerPrefix(tokenizer.NIL, p.parseNilLiteral)
	p.registerPrefix(tokenizer.LPAREN, p.parseGroupedOrLambda)
	p.registerPrefix(tokenizer.PIPE, p.parseLambda)     // Rust-style lambda: |x| expr
	p.registerPrefix(tokenizer.MATCH, p.parseMatchExpr) // Match expressions
	p.registerPrefix(tokenizer.STAR, p.parseUnaryExpr)  // *x (dereference)
	p.registerPrefix(tokenizer.MINUS, p.parseUnaryExpr) // -x (negation)
	p.registerPrefix(tokenizer.NOT, p.parseUnaryExpr)   // !x (logical not)
	p.registerPrefix(tokenizer.AND, p.parseUnaryExpr)   // &x (address-of)

	// Register infix parse functions for Dingo operators
	p.registerInfix(tokenizer.QUESTION, p.parseQuestionOperator)
	p.registerInfix(tokenizer.QUESTION_QUESTION, p.parseNullCoalescing)
	p.registerInfix(tokenizer.QUESTION_DOT, p.parseSafeNavigation)

	// Register infix parse functions for standard Go operators
	p.registerInfix(tokenizer.DOT, p.parseSelectorExpr)
	p.registerInfix(tokenizer.LPAREN, p.parseCallExpr)
	p.registerInfix(tokenizer.LBRACKET, p.parseIndexExpr)

	// Register binary operators
	binaryOps := []tokenizer.TokenKind{
		tokenizer.OR, tokenizer.AND,
		tokenizer.EQ, tokenizer.NE, tokenizer.LT, tokenizer.GT, tokenizer.LE, tokenizer.GE,
		tokenizer.PLUS, tokenizer.MINUS, tokenizer.STAR, tokenizer.SLASH,
		tokenizer.PIPE, // | (bitwise or) — must coexist with PIPE as a prefix lambda starter
	}
	for _, op := range binaryOps {
		p.registerInfix(op, p.parseBinaryExpr)
	}

	// Initialize current and peek tokens
	p.nextToken()
	p.nextToken()

	return p
}

// registerPrefix registers a prefix parse function for a token type
func (p *PrattParser) registerPrefix(tokenType tokenizer.TokenKind, fn prefixParseFn) {
	p.prefixParseFns[tokenType] = fn
}

// registerInfix registers an infix parse function for a token type
func (p *PrattParser) registerInfix(tokenType tokenizer.TokenKind, fn infixParseFn) {
	p.infixParseFns[tokenType] = fn
}

// nextToken advances to the next token
func (p *PrattParser) nextToken() {
	p.curToken = p.peekToken
	p.peekToken = p.tokenizer.NextToken()
}

// parserState captures the full state for lookahead/backtracking
type parserState struct {
	curToken  tokenizer.Token
	peekToken tokenizer.Token
	tokPos    int
}

// saveState saves the current parser state for backtracking
func (p *PrattParser) saveState() parserState {
	return parserState{
		curToken:  p.curToken,
		peekToken: p.peekToken,
		tokPos:    p.tokenizer.SavePos(),
	}
}

// restoreState restores a previously saved parser state
func (p *PrattParser) restoreState(state parserState) {
	p.curToken = state.curToken
	p.peekToken = state.peekToken
	p.tokenizer.RestorePos(state.tokPos)
}

// source returns the original source bytes for extracting text.
func (p *PrattParser) source() []byte {
	return p.tokenizer.Source()
}

// curTokenIs checks if current token is of given type
func (p *PrattParser) curTokenIs(t tokenizer.TokenKind) bool {
	return p.curToken.Kind == t
}

// peekTokenIs checks if peek token is of given type
func (p *PrattParser) peekTokenIs(t tokenizer.TokenKind) bool {
	return p.peekToken.Kind == t
}

// expectPeek advances if peek token is expected type
func (p *PrattParser) expectPeek(t tokenizer.TokenKind) bool {
	if p.peekTokenIs(t) {
		p.nextToken()
		return true
	}
	p.peekError(t)
	return false
}

// peekPrecedence returns the precedence of the peek token
func (p *PrattParser) peekPrecedence() int {
	if prec, ok := operatorPrecedence[p.peekToken.Kind]; ok {
		return prec
	}
	return PrecLowest
}

// curPrecedence returns the precedence of the current token
func (p *PrattParser) curPrecedence() int {
	if prec, ok := operatorPrecedence[p.curToken.Kind]; ok {
		return prec
	}
	return PrecLowest
}

// ParseExpression is the core Pratt parsing method
// minPrec is the minimum precedence level for operators to bind to the left expression
func (p *PrattParser) ParseExpression(minPrec int) ast.Expr {
	// Parse prefix expression (literals, identifiers, unary ops, grouped expressions)
	prefix := p.prefixParseFns[p.curToken.Kind]
	if prefix == nil {
		p.noPrefixParseFnError(p.curToken.Kind)
		return nil
	}

	leftExpr := prefix()

	// Parse infix/postfix expressions while precedence allows
	for !p.peekTokenIs(tokenizer.EOF) && !p.peekTokenIs(tokenizer.SEMICOLON) && minPrec < p.peekPrecedence() {
		infix := p.infixParseFns[p.peekToken.Kind]
		if infix == nil {
			return leftExpr
		}

		p.nextToken()
		leftExpr = infix(leftExpr)
	}

	return leftExpr
}

// Prefix parse functions

func (p *PrattParser) parseIdentifier() ast.Expr {
	// Check if this is a single-param TypeScript lambda: x => expr
	if p.peekTokenIs(tokenizer.ARROW) {
		return p.parseLambda()
	}

	// Return a DingoIdent node
	return &ast.DingoIdent{
		NamePos: p.curToken.Pos,
		Name:    p.curToken.Lit,
	}
}

func (p *PrattParser) parseIntegerLiteral() ast.Expr {
	return &ast.RawExpr{
		StartPos: p.curToken.Pos,
		EndPos:   p.curToken.End,
		Text:     p.curToken.Lit,
	}
}

func (p *PrattParser) parseFloatLiteral() ast.Expr {
	// TODO: Return proper ast.BasicLit node
	return nil
}

func (p *PrattParser) parseStringLiteral() ast.Expr {
	return &ast.RawExpr{
		StartPos: p.curToken.Pos,
		EndPos:   p.curToken.End,
		Text:     p.curToken.Lit,
	}
}

func (p *PrattParser) parseBoolLiteral() ast.Expr {
	lit := p.curToken.Lit
	if lit == "" {
		// Fallback if tokenizer doesn't provide literal text
		if p.curToken.Kind == tokenizer.TRUE {
			lit = "true"
		} else {
			lit = "false"
		}
	}
	return &ast.RawExpr{
		StartPos: p.curToken.Pos,
		EndPos:   p.curToken.End,
		Text:     lit,
	}
}

func (p *PrattParser) parseNilLiteral() ast.Expr {
	return &ast.RawExpr{
		StartPos: p.curToken.Pos,
		EndPos:   p.curToken.End,
		Text:     "nil",
	}
}

// parseUnaryExpr handles unary operators: *, -, !, &
func (p *PrattParser) parseUnaryExpr() ast.Expr {
	opToken := p.curToken
	opLit := opToken.Lit
	if opLit == "" {
		// Use token kind as literal if tokenizer doesn't provide it
		switch opToken.Kind {
		case tokenizer.STAR:
			opLit = "*"
		case tokenizer.MINUS:
			opLit = "-"
		case tokenizer.NOT:
			opLit = "!"
		case tokenizer.AND:
			opLit = "&"
		}
	}

	p.nextToken() // consume operator

	// Parse operand with high precedence (unary binds tightly)
	operand := p.ParseExpression(PrecUnary)

	return &ast.RawExpr{
		StartPos: opToken.Pos,
		EndPos:   operand.End(),
		Text:     opLit + operand.String(),
	}
}

// parseGroupedOrLambda handles grouped expressions, TypeScript lambdas, and tuple literals
// Performs lookahead to distinguish:
//   - (params) => body  -> TypeScript lambda
//   - (expr1, expr2)    -> Tuple literal
//   - (expr)            -> Grouped expression
func (p *PrattParser) parseGroupedOrLambda() ast.Expr {
	// Check if this is a TypeScript lambda
	lambda := p.parseLambda()
	if lambda != nil {
		return lambda
	}

	// Not a lambda, parse as grouped expression or tuple
	return p.parseGroupedOrTuple()
}

// parseGroupedOrTuple disambiguates grouped expressions from tuple literals
// Detection strategy:
//   - (expr,    -> tuple literal (comma after first element)
//   - (expr)    -> grouped expression (closing paren after first element)
func (p *PrattParser) parseGroupedOrTuple() ast.Expr {
	lparenPos := p.curToken.Pos
	p.nextToken() // consume '('

	// Parse first element
	first := p.ParseExpression(PrecLowest)

	// Check for comma -> tuple literal
	if p.peekTokenIs(tokenizer.COMMA) {
		return p.finishTupleLiteral(lparenPos, first)
	}

	// Single element -> grouped expression
	if !p.expectPeek(tokenizer.RPAREN) {
		return nil
	}

	return first
}

// finishTupleLiteral completes tuple literal parsing after detecting comma
// Called when we've parsed (first_expr and see a comma
func (p *PrattParser) finishTupleLiteral(lparen token.Pos, first ast.Expr) *ast.TupleLiteral {
	elements := []ast.Element{{Expr: first}}

	// Parse remaining elements
	for p.peekTokenIs(tokenizer.COMMA) {
		p.nextToken() // consume ','
		p.nextToken() // move to next element

		// Check for nested tuple
		var elem ast.Element
		if p.curTokenIs(tokenizer.LPAREN) {
			// Nested tuple - recursively parse
			nested := p.parseGroupedOrTuple()
			if nested == nil {
				// Parse error in nested expression - propagate
				return nil
			}
			if tupleLit, ok := nested.(*ast.TupleLiteral); ok {
				elem = ast.Element{Nested: tupleLit}
			} else {
				// If not a tuple, treat as regular expression
				elem = ast.Element{Expr: nested}
			}
		} else {
			// Regular expression
			expr := p.ParseExpression(PrecLowest)
			if expr == nil {
				// Parse error - propagate
				return nil
			}
			elem = ast.Element{Expr: expr}
		}

		elements = append(elements, elem)
	}

	if !p.expectPeek(tokenizer.RPAREN) {
		return nil
	}

	lit := &ast.TupleLiteral{
		Lparen:   lparen,
		Elements: elements,
		Rparen:   p.curToken.Pos,
	}

	// Notify collector if callback is registered
	if p.OnTupleLiteral != nil {
		p.OnTupleLiteral(lit)
	}

	return lit
}

// Infix parse functions for Dingo operators

// parseErrorPropagation handles the postfix ? operator (x?) with optional error transformation.
//
// Syntax patterns:
//   - expr?                      (basic - propagate error as-is)
//   - expr ? "message"           (context wrapping with fmt.Errorf)
//   - expr ? |err| transform     (Rust-style lambda transform)
//   - expr ? (err) => transform  (TypeScript-style lambda transform)
//   - expr ? err => transform    (TypeScript single-param lambda)
//
// Examples:
//
//	value := getData()?
//	order := fetchOrder(id) ? "fetch failed"
//	user := loadUser(id) ? |err| wrap("user", err)
//	config := loadConfig(path) ? (e) => fmt.Errorf("config: %w", e)
func (p *PrattParser) parseErrorPropagation(left ast.Expr) ast.Expr {
	questionPos := p.curToken.Pos
	p.nextToken() // Consume the ? token

	expr := &ast.ErrorPropExpr{
		Question: questionPos,
		Operand:  left,
		// ResultType and ErrorType will be filled by type checker during semantic analysis
	}

	// Pattern 1: ? "message" (string context)
	if p.curTokenIs(tokenizer.STRING) {
		msg := p.curToken.Lit
		// Strip quotes if tokenizer includes them
		if len(msg) >= 2 && msg[0] == '"' && msg[len(msg)-1] == '"' {
			msg = msg[1 : len(msg)-1]
		} else if len(msg) >= 2 && msg[0] == '`' && msg[len(msg)-1] == '`' {
			// Raw string literals
			msg = msg[1 : len(msg)-1]
		}
		expr.ErrorContext = &ast.ErrorContext{
			Message:    msg,
			MessagePos: p.curToken.Pos,
		}
		p.nextToken() // consume string
		p.collectDingoNode(expr)
		return expr
	}

	// Pattern 2: ? |err| transform (Rust-style lambda)
	if p.curTokenIs(tokenizer.PIPE) {
		lambda := p.parseRustLambda()
		if lambda != nil {
			expr.ErrorTransform = lambda.(*ast.LambdaExpr)
		}
		p.collectDingoNode(expr)
		return expr
	}

	// Pattern 3: ? (err) => transform (TypeScript-style lambda with parens)
	if p.curTokenIs(tokenizer.LPAREN) && p.isTypeScriptLambda() {
		lambda := p.parseTSLambda()
		if lambda != nil {
			expr.ErrorTransform = lambda.(*ast.LambdaExpr)
		}
		p.collectDingoNode(expr)
		return expr
	}

	// Pattern 4: ? err => transform (TypeScript single-param without parens)
	if p.curTokenIs(tokenizer.IDENT) && p.peekTokenIs(tokenizer.ARROW) {
		lambda := p.parseTSSingleParamLambda()
		if lambda != nil {
			expr.ErrorTransform = lambda.(*ast.LambdaExpr)
		}
		p.collectDingoNode(expr)
		return expr
	}

	// Pattern 5: basic ? (no transformation)
	p.collectDingoNode(expr)
	return expr
}

// parseNullCoalescing handles the infix ?? operator (a ?? b)
// Right-associative: a ?? b ?? c is parsed as a ?? (b ?? c)
func (p *PrattParser) parseNullCoalescing(left ast.Expr) ast.Expr {
	opPos := p.curToken.Pos
	opLine := p.curToken.Line
	opColumn := p.curToken.Column
	precedence := p.curPrecedence()

	// Move to right operand
	p.nextToken()

	// Right-associative: use same precedence (not precedence + 1)
	// This makes a ?? b ?? c parse as a ?? (b ?? c)
	right := p.ParseExpression(precedence)

	// Check for missing right operand
	if right == nil {
		p.errors = append(p.errors, SpanError{
			Pos:     opPos,
			Line:    opLine,
			Column:  opColumn,
			Code:    ErrMissingOperand,
			Message: "expected expression after '??' (null coalescing operator requires a default value)",
			Hint:    "provide a default value: expr ?? defaultValue",
		})
		return left // Return left to allow partial recovery
	}

	expr := &ast.NullCoalesceExpr{
		Left:  left,
		OpPos: opPos,
		Right: right,
	}
	p.collectDingoNode(expr)
	return expr
}

// parseSafeNavigation handles the postfix ?. operator (x?.field or x?.method(args))
func (p *PrattParser) parseSafeNavigation(left ast.Expr) ast.Expr {
	opPos := p.curToken.Pos

	// After ?., we expect a field name or method call
	if !p.expectPeek(tokenizer.IDENT) {
		return nil
	}

	// Create identifier using Dingo AST
	sel := &ast.DingoIdent{
		NamePos: p.curToken.Pos,
		Name:    p.curToken.Lit,
	}

	// Check if this is a method call (next token is LPAREN)
	if p.peekTokenIs(tokenizer.LPAREN) {
		// Method call: x?.method(args)
		p.nextToken() // consume IDENT
		p.nextToken() // consume LPAREN

		// Parse arguments
		args := []ast.Expr{}
		if !p.curTokenIs(tokenizer.RPAREN) {
			// Parse first argument
			arg := p.ParseExpression(PrecLowest)
			if arg != nil {
				args = append(args, arg)
			}

			// Parse remaining arguments
			for p.peekTokenIs(tokenizer.COMMA) {
				p.nextToken() // consume comma
				p.nextToken() // move to next argument
				arg := p.ParseExpression(PrecLowest)
				if arg != nil {
					args = append(args, arg)
				}
			}
		}

		// Expect closing paren
		if !p.expectPeek(tokenizer.RPAREN) {
			return nil
		}

		callExpr := &ast.SafeNavCallExpr{
			X:     left,
			OpPos: opPos,
			Fun:   sel,
			Args:  args,
		}
		p.collectDingoNode(callExpr)
		return callExpr
	}

	// Field access: x?.field
	navExpr := &ast.SafeNavExpr{
		X:     left,
		OpPos: opPos,
		Sel:   sel,
	}
	p.collectDingoNode(navExpr)
	return navExpr
}

// Error handling

func (p *PrattParser) noPrefixParseFnError(t tokenizer.TokenKind) {
	hint := GetHintForToken(t)
	p.errors = append(p.errors, SpanError{
		Pos:     p.curToken.Pos,
		EndPos:  p.curToken.End,
		Line:    p.curToken.Line,
		Column:  p.curToken.Column,
		Code:    ErrUnexpectedToken,
		Message: fmt.Sprintf("unexpected token '%s' at start of expression", t),
		Hint:    hint,
	})
}

// addSpanError adds an error with start/end positions
func (p *PrattParser) addSpanError(startPos, endPos token.Pos, msg, hint string) {
	p.errors = append(p.errors, SpanError{
		Pos:     startPos,
		EndPos:  endPos,
		Line:    p.curToken.Line,
		Column:  p.curToken.Column,
		Message: msg,
		Hint:    hint,
	})
}

// addErrorWithCode adds an error with an error code
func (p *PrattParser) addErrorWithCode(code, msg, hint string) {
	p.errors = append(p.errors, SpanError{
		Pos:     p.curToken.Pos,
		EndPos:  p.curToken.End,
		Line:    p.curToken.Line,
		Column:  p.curToken.Column,
		Code:    code,
		Message: msg,
		Hint:    hint,
	})
}

// synchronize advances tokens until a sync point is found in peek position
// Used for error recovery to continue parsing after an error
// After synchronize returns, peekToken will be at a sync point (or EOF)
func (p *PrattParser) synchronize(syncSet SyncSet) {
	for !p.peekTokenIs(tokenizer.EOF) {
		// Check if peek token is a sync point
		if syncSet.Contains(p.peekToken.Kind) {
			return
		}
		// Check if peek token starts a new construct
		if p.peekTokenIs(tokenizer.MATCH) || p.peekTokenIs(tokenizer.FUNC) {
			return
		}
		p.nextToken()
	}
}

func (p *PrattParser) peekError(t tokenizer.TokenKind) {
	msg := fmt.Sprintf("expected next token to be %s, got %s instead", t, p.peekToken.Kind)
	p.errors = append(p.errors, SpanError{
		Pos:     p.peekToken.Pos,
		EndPos:  p.peekToken.End,
		Line:    p.peekToken.Line,
		Column:  p.peekToken.Column,
		Code:    ErrUnexpectedToken,
		Message: msg,
	})
}

// Errors returns all parse errors encountered
func (p *PrattParser) Errors() []ParseError {
	return p.errors
}

// collectDingoNode notifies the callback to collect a Dingo expression node.
// If expr implements DingoNode directly, it's passed as-is.
// Otherwise, it's wrapped in an ExprWrapper.
func (p *PrattParser) collectDingoNode(expr ast.Expr) {
	if p.OnDingoNode == nil {
		return
	}
	// First check if expr directly implements DingoNode
	if node, ok := expr.(ast.DingoNode); ok {
		p.OnDingoNode(node)
		return
	}
	// Otherwise wrap in ExprWrapper
	p.OnDingoNode(&ast.ExprWrapper{DingoExpr: expr})
}

// parseSelectorExpr handles the infix DOT operator (x.field or pkg.Func)
// For qualified identifiers like fmt.Sprintf, this builds the full expression
func (p *PrattParser) parseSelectorExpr(left ast.Expr) ast.Expr {
	// Current token is DOT (not used in RawExpr approach)
	_ = p.curToken.Pos

	// Expect identifier after DOT
	if !p.expectPeek(tokenizer.IDENT) {
		p.addError("expected identifier after '.'")
		return nil
	}

	// Get the selector identifier
	selName := p.curToken.Lit
	selEnd := p.curToken.End

	// Build the full selector expression as RawExpr
	// We need to reconstruct the full text: left.selector
	leftText := ""
	if ident, ok := left.(*ast.DingoIdent); ok {
		leftText = ident.Name
	} else if raw, ok := left.(*ast.RawExpr); ok {
		leftText = raw.Text
	} else {
		// Fallback: use left's String() method
		leftText = left.String()
	}

	fullText := leftText + "." + selName

	return &ast.RawExpr{
		StartPos: left.Pos(),
		EndPos:   selEnd,
		Text:     fullText,
	}
}

// parseCallExpr handles the infix LPAREN operator (function calls)
// This allows parsing expressions like fmt.Sprintf("hello %s", name)
//
// For built-in functions (len, cap) with Dingo expressions in arguments,
// returns BuiltinCallExpr to enable special code generation with hoisting.
func (p *PrattParser) parseCallExpr(left ast.Expr) ast.Expr {
	lparenPos := p.curToken.Pos
	if left == nil {
		return nil
	}
	funcPos := left.Pos()

	// Check if this is a built-in function call (len or cap)
	funcName := ""
	if ident, ok := left.(*ast.DingoIdent); ok {
		funcName = ident.Name
	} else if raw, ok := left.(*ast.RawExpr); ok {
		funcName = raw.Text
	}
	isBuiltin := funcName == "len" || funcName == "cap"

	// Parse arguments as Expr (preserving AST for Dingo expressions)
	var argsExpr []ast.Expr
	if !p.peekTokenIs(tokenizer.RPAREN) {
		// Parse first argument
		p.nextToken()
		arg := p.ParseExpression(PrecLowest)
		if arg != nil {
			argsExpr = append(argsExpr, arg)
		}

		// Parse remaining arguments
		for p.peekTokenIs(tokenizer.COMMA) {
			p.nextToken() // consume comma
			p.nextToken() // move to next argument
			arg := p.ParseExpression(PrecLowest)
			if arg != nil {
				argsExpr = append(argsExpr, arg)
			}
		}
	}

	// Expect closing paren
	if !p.expectPeek(tokenizer.RPAREN) {
		return nil
	}
	rparenPos := p.curToken.End

	// For built-in functions with Dingo expressions, return BuiltinCallExpr
	if isBuiltin {
		builtinExpr := &ast.BuiltinCallExpr{
			Func:    funcName,
			FuncPos: funcPos,
			Args:    argsExpr,
			RParen:  rparenPos,
		}
		// Only return BuiltinCallExpr if it contains Dingo expressions
		// Otherwise fall through to RawExpr for simpler code generation
		if builtinExpr.ContainsDingoExpr() {
			return builtinExpr
		}
	}

	// Build the full call expression as RawExpr (default path)
	funcText := left.String()
	argsText := ""
	for i, arg := range argsExpr {
		if i > 0 {
			argsText += ", "
		}
		argsText += arg.String()
	}

	fullText := funcText + "(" + argsText + ")"

	_ = lparenPos // suppress unused warning

	return &ast.RawExpr{
		StartPos: left.Pos(),
		EndPos:   rparenPos,
		Text:     fullText,
	}
}

// parseIndexExpr handles the infix LBRACKET operator (index/slice expressions)
// This allows parsing expressions like points[0] or arr[1:3]
func (p *PrattParser) parseIndexExpr(left ast.Expr) ast.Expr {
	// Current token is LBRACKET '['
	lbracketPos := p.curToken.Pos

	p.nextToken() // consume '['

	// Parse the index expression
	index := p.ParseExpression(PrecLowest)

	// Expect closing bracket
	if !p.expectPeek(tokenizer.RBRACKET) {
		p.addError("expected ']' after index expression")
		return nil
	}
	rbracketPos := p.curToken.End

	// Build the full index expression as RawExpr
	leftText := ""
	if ident, ok := left.(*ast.DingoIdent); ok {
		leftText = ident.Name
	} else if raw, ok := left.(*ast.RawExpr); ok {
		leftText = raw.Text
	} else {
		leftText = left.String()
	}

	indexText := ""
	if index != nil {
		indexText = index.String()
	}

	fullText := leftText + "[" + indexText + "]"

	_ = lbracketPos // suppress unused warning

	return &ast.RawExpr{
		StartPos: left.Pos(),
		EndPos:   rbracketPos,
		Text:     fullText,
	}
}

// addError is a helper to add errors with current token position
func (p *PrattParser) addError(msg string) {
	p.errors = append(p.errors, SpanError{
		Pos:     p.curToken.Pos,
		EndPos:  p.curToken.End,
		Line:    p.curToken.Line,
		Column:  p.curToken.Column,
		Message: msg,
	})
}

// parseQuestionOperator handles both error propagation (?) and ternary (? :)
// Uses structured decision table from classifyQuestionOperator for clean disambiguation
//
// Patterns:
//   - expr?                        -> error propagation (postfix)
//   - expr ? "message"             -> error propagation with context
//   - expr ? |err| transform       -> error propagation with Rust-style lambda
//   - expr ? (e) => transform      -> error propagation with TS-style lambda
//   - expr ? e => transform        -> error propagation with TS single-param lambda
//   - cond ? trueVal : falseVal    -> ternary operator
func (p *PrattParser) parseQuestionOperator(left ast.Expr) ast.Expr {
	questionPos := p.curToken.Pos
	state := p.saveState()

	p.nextToken() // consume ?

	// Skip newlines and comments after ?
	p.consumeNewlinesAndComments()

	// Classify using decision table
	result := p.classifyQuestionOperator()

	switch result.kind {
	case qkErrorPropPostfix:
		// Basic error propagation: expr?
		expr := &ast.ErrorPropExpr{
			Question: questionPos,
			Operand:  left,
		}
		p.collectDingoNode(expr)
		return expr

	case qkErrorWithContext, qkErrorWithRustLambda, qkErrorWithTSLambda:
		// All error propagation variants handled by parseErrorPropagation
		p.restoreState(state)
		return p.parseErrorPropagation(left)

	case qkTernary:
		// Ternary: cond ? trueVal : falseVal
		return p.parseTernary(left, questionPos)

	default:
		// Fallback to error propagation
		expr := &ast.ErrorPropExpr{
			Question: questionPos,
			Operand:  left,
		}
		p.collectDingoNode(expr)
		return expr
	}
}

// parseTernary handles cond ? trueVal : falseVal
// Called after ? is consumed and ternary is confirmed via classification
func (p *PrattParser) parseTernary(cond ast.Expr, questionPos token.Pos) ast.Expr {
	// Parse true branch with higher precedence
	// Use higher precedence (PrecTernary + 1) to prevent consuming the colon
	trueExpr := p.ParseExpression(PrecTernary + 1)

	// Skip newlines and comments before checking for colon
	p.consumePeekNewlinesAndComments()

	// Expect colon
	if !p.peekTokenIs(tokenizer.COLON) {
		p.addErrorWithCode(ErrMissingColon,
			"ternary operator missing ':'",
			"ternary syntax: condition ? trueValue : falseValue")
		return nil
	}

	p.nextToken() // move to :
	colonPos := p.curToken.Pos
	p.nextToken() // consume :

	// Skip newlines and comments after :
	p.consumeNewlinesAndComments()

	// Right-associative: parse false branch at lower precedence
	// This makes a ? b : c ? d : e parse as a ? b : (c ? d : e)
	// Use PrecTernary - 1 so the loop condition (minPrec < peekPrec) allows
	// the next ternary to bind: (PrecTernary - 1) < PrecTernary = true
	falseExpr := p.ParseExpression(PrecTernary - 1)

	ternaryExpr := &ast.TernaryExpr{
		Cond:     cond,
		Question: questionPos,
		True:     trueExpr,
		Colon:    colonPos,
		False:    falseExpr,
	}
	p.collectDingoNode(ternaryExpr)
	return ternaryExpr
}

// isExpressionTerminator returns true if current token ends an expression
func (p *PrattParser) isExpressionTerminator() bool {
	switch p.curToken.Kind {
	case tokenizer.EOF, tokenizer.SEMICOLON, tokenizer.RPAREN,
		tokenizer.RBRACE, tokenizer.COMMA, tokenizer.COLON,
		tokenizer.RBRACKET, tokenizer.NEWLINE:
		return true
	}
	return false
}

// consumeNewlinesAndComments consumes all NEWLINE and COMMENT tokens at current position.
// This allows multi-line ternary expressions with optional inline comments.
// Returns the number of tokens skipped.
func (p *PrattParser) consumeNewlinesAndComments() int {
	count := 0
	for p.curTokenIs(tokenizer.NEWLINE) || p.curTokenIs(tokenizer.COMMENT) {
		p.nextToken()
		count++
	}
	return count
}

// consumePeekNewlinesAndComments advances past any NEWLINE/COMMENT in peek position.
// Useful when checking what token comes after newlines/comments.
func (p *PrattParser) consumePeekNewlinesAndComments() int {
	count := 0
	for p.peekTokenIs(tokenizer.NEWLINE) || p.peekTokenIs(tokenizer.COMMENT) {
		p.nextToken()
		count++
	}
	return count
}

// parseBinaryExpr parses binary expressions (left-associative)
// Handles operators like: +, -, *, /, ==, !=, <, >, <=, >=, &&, ||
//
// If either operand contains Dingo expressions (SafeNavExpr, BuiltinCallExpr, etc.),
// returns a BinaryExpr to preserve the AST structure for codegen.
// Otherwise, returns a RawExpr for simpler processing.
func (p *PrattParser) parseBinaryExpr(left ast.Expr) ast.Expr {
	// Handle nil left operand (parse error occurred)
	if left == nil {
		return nil
	}

	opToken := p.curToken
	precedence := p.curPrecedence()

	p.nextToken() // consume operator

	// Left-associative: parse right operand at precedence + 1
	// This prevents same-precedence operators from binding on the right
	// and allows lower-precedence operators (like ternary) to bind at top level
	right := p.ParseExpression(precedence + 1)

	// Handle nil right operand (parse error occurred)
	if right == nil {
		return nil
	}

	// If either operand contains Dingo expressions, preserve AST structure
	// This enables proper code generation for nested expressions
	if containsDingoExpr(left) || containsDingoExpr(right) {
		return &ast.BinaryExpr{
			X:     left,
			OpPos: opToken.Pos,
			Op:    opToken.Lit,
			Y:     right,
		}
	}

	// For simple expressions (no Dingo syntax), use RawExpr for efficiency
	leftStr := p.exprToString(left)
	rightStr := p.exprToString(right)

	return &ast.RawExpr{
		StartPos: left.Pos(),
		EndPos:   right.End(),
		Text:     leftStr + " " + opToken.Lit + " " + rightStr,
	}
}

// containsDingoExpr checks if an expression contains Dingo-specific syntax
func containsDingoExpr(expr ast.Expr) bool {
	if expr == nil {
		return false
	}
	switch e := expr.(type) {
	case *ast.SafeNavExpr, *ast.SafeNavCallExpr, *ast.NullCoalesceExpr,
		*ast.TernaryExpr, *ast.MatchExpr, *ast.LambdaExpr, *ast.ErrorPropExpr:
		return true
	case *ast.BuiltinCallExpr:
		return e.ContainsDingoExpr()
	case *ast.BinaryExpr:
		return e.ContainsDingoExpr()
	default:
		return false
	}
}

// exprToString converts an AST expression to its string representation
func (p *PrattParser) exprToString(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.DingoIdent:
		return e.Name
	case *ast.RawExpr:
		return e.Text
	default:
		// Fallback to String() method if available
		if stringer, ok := expr.(interface{ String() string }); ok {
			return stringer.String()
		}
		return ""
	}
}
