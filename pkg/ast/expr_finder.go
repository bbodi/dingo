package ast

import (
	"fmt"

	"github.com/MadAppGang/dingo/pkg/tokenizer"
)

// ExprKind represents the type of Dingo expression
type ExprKind int

const (
	ExprMatch        ExprKind = iota // match expr { ... }
	ExprLambdaRust                   // |x| body
	ExprLambdaTS                     // (x) => body
	ExprErrorProp                    // expr?
	ExprNullCoalesce                 // expr ?? default
	ExprSafeNav                      // expr?.field
	ExprTernary                      // cond ? a : b
)

// String returns string representation of ExprKind
func (k ExprKind) String() string {
	switch k {
	case ExprMatch:
		return "match"
	case ExprLambdaRust:
		return "lambda(rust)"
	case ExprLambdaTS:
		return "lambda(ts)"
	case ExprErrorProp:
		return "error_prop"
	case ExprNullCoalesce:
		return "null_coalesce"
	case ExprSafeNav:
		return "safe_nav"
	case ExprTernary:
		return "ternary"
	default:
		return fmt.Sprintf("ExprKind(%d)", k)
	}
}

// ExprContext represents the context where an expression appears
type ExprContext int

const (
	ContextStatement  ExprContext = iota // standalone statement
	ContextAssignment                    // result := expr
	ContextReturn                        // return expr
	ContextArgument                      // foo(expr)
)

// String returns string representation of ExprContext
func (c ExprContext) String() string {
	switch c {
	case ContextStatement:
		return "statement"
	case ContextAssignment:
		return "assignment"
	case ContextReturn:
		return "return"
	case ContextArgument:
		return "argument"
	default:
		return fmt.Sprintf("ExprContext(%d)", c)
	}
}

// ExprLocation represents the location and context of a Dingo expression
type ExprLocation struct {
	Kind    ExprKind
	Start   int         // byte offset (inclusive)
	End     int         // byte offset (exclusive)
	Context ExprContext // context where expression appears

	// Statement-level information for human-like code generation
	StatementStart int    // containing statement start byte offset
	StatementEnd   int    // containing statement end byte offset
	VarName        string // for assignments: the variable name being assigned
}

// FindDingoExpressions scans source code and returns all match and lambda expression locations
func FindDingoExpressions(src []byte) ([]ExprLocation, error) {
	tok := tokenizer.New(src)
	allTokens, err := tok.Tokenize()
	if err != nil {
		return nil, fmt.Errorf("tokenize: %w", err)
	}

	var locations []ExprLocation
	tok.Reset()

	for {
		current := tok.Current()
		if current.Kind == tokenizer.EOF {
			break
		}

		// Skip comments and strings (never look for expressions inside)
		if current.Kind == tokenizer.COMMENT || current.Kind == tokenizer.STRING || current.Kind == tokenizer.CHAR {
			tok.Advance()
			continue
		}

		// Match expression
		if current.Kind == tokenizer.MATCH {
			// Context check: `match` is also a perfectly valid Go
			// identifier, and codebases like the Go compiler use it
			// as a method or function name. Skip the MATCH token in
			// positions that can only be an identifier:
			//   - `x.match(...)` — selector before, so this is a
			//     method call. Previous significant token is `.`.
			//   - `func match(...)` — function decl. Previous is FUNC.
			//   - `func (r T) match(...)` — method decl, where the
			//     receiver list closes with `)`. Previous is RPAREN.
			// In all three cases, treating MATCH as a keyword would
			// later fail to find the `{ arms }` body and abort the
			// transpile with an `expected '{'` / `unmatched closing
			// brace` error.
			currentIdx := -1
			for i, t := range allTokens {
				if t.BytePos() == current.BytePos() {
					currentIdx = i
					break
				}
			}
			if currentIdx > 0 {
				skip := false
				for j := currentIdx - 1; j >= 0; j-- {
					switch allTokens[j].Kind {
					case tokenizer.NEWLINE, tokenizer.SEMICOLON, tokenizer.COMMENT:
						continue
					case tokenizer.DOT, tokenizer.FUNC, tokenizer.RPAREN:
						skip = true
					}
					break
				}
				if skip {
					tok.Advance()
					continue
				}
			}

			// Forward lookahead: a real match expression has the shape
			// `match <scrutinee> { <pat> => <body>, ... }`. To accept it
			// here, we require BOTH:
			//   (a) a top-level `{` after `match` before any statement
			//       terminator (so bare calls like `_ = match(7)` are
			//       rejected — the call's `(` and `)` open and close at
			//       depth 1, then no `{` follows at depth 0),
			//   (b) an `=>` token inside the `{ ... }` body (so
			//       `switch match { ... }`, `if match { ... }`, etc.
			//       where `match` is an ordinary variable name are not
			//       mis-detected — switch/if bodies use `case` or plain
			//       statements, never `=>`).
			//
			// The two checks together let us treat `match` as an
			// identifier in all the non-keyword contexts the Go compiler
			// uses (variable name, method name, function name) while
			// preserving real match-expression detection.
			if currentIdx >= 0 {
				openBraceIdx := -1
				parenD, brackD := 0, 0
			scan:
				for j := currentIdx + 1; j < len(allTokens); j++ {
					switch allTokens[j].Kind {
					case tokenizer.LPAREN:
						parenD++
					case tokenizer.RPAREN:
						parenD--
						if parenD < 0 {
							break scan
						}
					case tokenizer.LBRACKET:
						brackD++
					case tokenizer.RBRACKET:
						brackD--
						if brackD < 0 {
							break scan
						}
					case tokenizer.LBRACE:
						if parenD == 0 && brackD == 0 {
							openBraceIdx = j
						}
						break scan
					case tokenizer.RBRACE:
						if parenD == 0 && brackD == 0 {
							break scan
						}
					case tokenizer.SEMICOLON, tokenizer.EOF:
						if parenD == 0 && brackD == 0 {
							break scan
						}
					}
				}
				if openBraceIdx < 0 {
					tok.Advance()
					continue
				}
				// Peek inside the `{ ... }` for an arrow. We scan with
				// brace-depth tracking so a nested `{}` inside a body
				// expression doesn't end the search early.
				hasArrow := false
				braceD := 1
				for j := openBraceIdx + 1; j < len(allTokens) && braceD > 0; j++ {
					switch allTokens[j].Kind {
					case tokenizer.LBRACE:
						braceD++
					case tokenizer.RBRACE:
						braceD--
					case tokenizer.ARROW:
						if braceD == 1 {
							hasArrow = true
							braceD = 0 // break
						}
					}
				}
				if !hasArrow {
					tok.Advance()
					continue
				}
			}

			start := current.BytePos()
			tok.Advance() // consume 'match'

			// Find the end of match expression
			end, err := findMatchEnd(tok, start)
			if err != nil {
				return nil, err
			}

			// Get full context with statement boundaries using tokens
			// Find the token index for the match keyword
			matchTokenIdx := 0
			for i, t := range allTokens {
				if t.BytePos() == start {
					matchTokenIdx = i
					break
				}
			}
			ctxInfo := detectContextFromTokens(allTokens, matchTokenIdx, end)

			locations = append(locations, ExprLocation{
				Kind:           ExprMatch,
				Start:          start,
				End:            end,
				Context:        ctxInfo.Context,
				StatementStart: ctxInfo.StatementStart,
				StatementEnd:   ctxInfo.StatementEnd,
				VarName:        ctxInfo.VarName,
			})
			continue
		}

		// Rust-style lambda: |params| body
		if current.Kind == tokenizer.PIPE {
			// Peek ahead to distinguish from bitwise OR operator
			// A Rust lambda has the pattern: |params| body
			// Bitwise OR has the pattern: expr | expr (no closing pipe)
			// We need to verify there's a closing | before any statement terminator
			if isRustLambda(tok, allTokens) {
				start := current.BytePos()
				tok.Advance() // consume opening |

				end, err := findLambdaEnd(tok, ExprLambdaRust)
				if err != nil {
					return nil, err
				}

				context := detectContext(src, start)

				locations = append(locations, ExprLocation{
					Kind:    ExprLambdaRust,
					Start:   start,
					End:     end,
					Context: context,
				})
				continue
			}
		}

		// TypeScript-style lambda: (params) => body or (params): RetType => body
		// After TransformSource, this looks like: (params) => or (params) RetType =>
		// Need to look for pattern: ( ... ) [optional IDENT] =>
		if current.Kind == tokenizer.LPAREN {
			// Save position to potentially rewind
			savedPos := tok.Current()
			tok.Advance() // consume (

			// Try to find => pattern
			isLambda := false
			depth := 1
			for depth > 0 {
				t := tok.Current()
				if t.Kind == tokenizer.EOF {
					break
				}
				if t.Kind == tokenizer.LPAREN {
					depth++
				}
				if t.Kind == tokenizer.RPAREN {
					depth--
					if depth == 0 {
						// Check if next is => directly, or IDENT (return type) then =>
						tok.Advance()
						if tok.Current().Kind == tokenizer.ARROW {
							isLambda = true
						} else if tok.Current().Kind == tokenizer.IDENT {
							// Could be return type annotation: ) RetType =>
							tok.Advance()
							if tok.Current().Kind == tokenizer.ARROW {
								isLambda = true
							}
						}
						break
					}
				}
				tok.Advance()
			}

			// Rewind to saved position
			tok.Reset()
			for tok.Current().Pos < savedPos.Pos {
				tok.Advance()
			}

			if isLambda {
				start := current.BytePos()
				tok.Advance() // consume (

				end, err := findLambdaEnd(tok, ExprLambdaTS)
				if err != nil {
					return nil, err
				}

				context := detectContext(src, start)

				locations = append(locations, ExprLocation{
					Kind:    ExprLambdaTS,
					Start:   start,
					End:     end,
					Context: context,
				})
				continue
			}
		}

		// Ternary operator: cond ? trueVal : falseVal
		// Check FIRST before error propagation, since we can distinguish by looking for :
		if current.Kind == tokenizer.QUESTION {
			next := tok.PeekToken()
			// Only standalone ? (not ?? or ?.)
			if next.Kind != tokenizer.QUESTION && next.Kind != tokenizer.DOT {
				// Find current token index in allTokens
				currentIdx := -1
				for i, t := range allTokens {
					if t.Pos == current.Pos {
						currentIdx = i
						break
					}
				}

				if currentIdx > 0 {
					// Look ahead to see if there's a matching :
					colonIdx := findMatchingColon(allTokens, currentIdx)
					if colonIdx > currentIdx {
						// This is a ternary! Find the full expression boundaries
						// Condition starts before ?
						condStart := findTernaryCondStart(allTokens, currentIdx, src)
						// False expression ends after :
						falseEnd := findTernaryFalseEnd(allTokens, colonIdx, src)

						// Get context
						ctxInfo := detectContextFromTokens(allTokens, findTokenIdxForBytePos(allTokens, condStart), falseEnd)

						locations = append(locations, ExprLocation{
							Kind:           ExprTernary,
							Start:          condStart,
							End:            falseEnd,
							Context:        ctxInfo.Context,
							StatementStart: ctxInfo.StatementStart,
							StatementEnd:   ctxInfo.StatementEnd,
							VarName:        ctxInfo.VarName,
						})
						// Skip past this ternary
						tok.Reset()
						for tok.Current().BytePos() < falseEnd && tok.Current().Kind != tokenizer.EOF {
							tok.Advance()
						}
						continue
					} else {
						// No matching colon, must be error propagation
						// Find operand start by scanning backward
						operandStart := findOperandStart(allTokens, currentIdx, src)

						locations = append(locations, ExprLocation{
							Kind:    ExprErrorProp,
							Start:   operandStart,
							End:     current.ByteEnd(), // After the ?
							Context: detectContext(src, operandStart),
						})
					}
				}
			}
		}

		// Null coalescing: expr ?? default
		if current.Kind == tokenizer.QUESTION_QUESTION {
			// Check if this position is already covered by a previous expression
			// (handles chained ?? like a ?? b ?? c - only detect once)
			alreadyCovered := false
			currentPos := current.BytePos()
			for _, loc := range locations {
				if loc.Kind == ExprNullCoalesce && currentPos >= loc.Start && currentPos < loc.End {
					alreadyCovered = true
					break
				}
			}
			if alreadyCovered {
				tok.Advance()
				continue
			}

			// Find current token index in allTokens
			currentIdx := -1
			for i, t := range allTokens {
				if t.Pos == current.Pos {
					currentIdx = i
					break
				}
			}

			if currentIdx > 0 {
				// Find operand start by scanning backward (returns byte pos and token index)
				operandStart, operandTokenIdx := findOperandStartWithIndex(allTokens, currentIdx, src)

				// Find right side end by scanning forward
				tok.Advance() // Skip ??
				rightEnd := findNullCoalesceEnd(tok, allTokens, currentIdx+1)

				// Get full context with statement boundaries using tokens
				ctxInfo := detectContextFromTokens(allTokens, operandTokenIdx, rightEnd)

				locations = append(locations, ExprLocation{
					Kind:           ExprNullCoalesce,
					Start:          operandStart,
					End:            rightEnd,
					Context:        ctxInfo.Context,
					StatementStart: ctxInfo.StatementStart,
					StatementEnd:   ctxInfo.StatementEnd,
					VarName:        ctxInfo.VarName,
				})
				continue // Already advanced
			}
		}

		// Safe navigation: expr?.field
		if current.Kind == tokenizer.QUESTION_DOT {
			// Find current token index in allTokens
			currentIdx := -1
			for i, t := range allTokens {
				if t.Pos == current.Pos {
					currentIdx = i
					break
				}
			}

			if currentIdx > 0 {
				// Find operand start by scanning backward (returns byte pos and token index)
				operandStart, operandTokenIdx := findOperandStartWithIndex(allTokens, currentIdx, src)

				// Save position in case we need to rewind
				savedPos := tok.Current()

				// Find safe nav chain end by scanning forward
				tok.Advance() // Skip ?.
				safeNavEnd := findSafeNavEnd(tok, allTokens, currentIdx+1)

				// If safeNavEnd is -1, this ?. is followed by ?? somewhere,
				// so we should skip this detection and let null coalesce handle it
				if safeNavEnd != -1 {
					// Get full context with statement boundaries using tokens
					ctxInfo := detectContextFromTokens(allTokens, operandTokenIdx, safeNavEnd)

					locations = append(locations, ExprLocation{
						Kind:           ExprSafeNav,
						Start:          operandStart,
						End:            safeNavEnd,
						Context:        ctxInfo.Context,
						StatementStart: ctxInfo.StatementStart,
						StatementEnd:   ctxInfo.StatementEnd,
						VarName:        ctxInfo.VarName,
					})
					continue // Already advanced
				} else {
					// Rewind - let the main loop advance normally
					// so ?? detection can find this expression
					tok.Reset()
					for tok.Current().Pos < savedPos.Pos {
						tok.Advance()
					}
				}
			}
		}

		tok.Advance()
	}

	return locations, nil
}

// findMatchEnd finds the closing brace of a match expression
// Assumes tok is positioned just after 'match' keyword
func findMatchEnd(tok *tokenizer.Tokenizer, startPos int) (int, error) {
	depth := 0
	foundOpen := false

	for {
		current := tok.Current()
		if current.Kind == tokenizer.EOF {
			return 0, fmt.Errorf("unexpected EOF in match expression starting at byte %d", startPos)
		}

		switch current.Kind {
		case tokenizer.LBRACE:
			foundOpen = true
			depth++
		case tokenizer.RBRACE:
			depth--
			if depth == 0 && foundOpen {
				tok.Advance()
				return current.ByteEnd(), nil // Position after closing brace
			}
			if depth < 0 {
				return 0, fmt.Errorf("unmatched closing brace at byte %d in match expression", current.BytePos())
			}
		}

		tok.Advance()
	}
}

// findLambdaEnd finds the end of a lambda expression body
// For Rust style: assumes tok is positioned after opening |
// For TS style: assumes tok is positioned after opening (
func findLambdaEnd(tok *tokenizer.Tokenizer, style ExprKind) (int, error) {
	braceDepth := 0
	parenDepth := 0
	bracketDepth := 0

	// Skip parameters first
	if style == ExprLambdaRust {
		// Skip to closing pipe
		for {
			current := tok.Current()
			if current.Kind == tokenizer.EOF {
				return 0, fmt.Errorf("unexpected EOF in lambda params")
			}
			if current.Kind == tokenizer.PIPE {
				tok.Advance()
				break
			}
			tok.Advance()
		}
	} else if style == ExprLambdaTS {
		// Skip to => arrow (already past opening paren)
		parenDepth = 1
		for parenDepth > 0 {
			current := tok.Current()
			if current.Kind == tokenizer.EOF {
				return 0, fmt.Errorf("unexpected EOF in lambda params")
			}
			if current.Kind == tokenizer.LPAREN {
				parenDepth++
			}
			if current.Kind == tokenizer.RPAREN {
				parenDepth--
			}
			tok.Advance()
		}

		// Expect => or IDENT (return type) then =>
		// After TransformSource, pattern is: ) RetType => or just ) =>
		current := tok.Current()
		if current.Kind == tokenizer.IDENT {
			// Skip optional return type annotation
			tok.Advance()
			current = tok.Current()
		}
		if current.Kind != tokenizer.ARROW {
			return 0, fmt.Errorf("expected => after lambda params, got %s", current.Kind)
		}
		tok.Advance()
		parenDepth = 0 // Reset for body parsing
	}

	// Now find end of body
	// Body can be:
	// - Block: { ... }
	// - Expression: stops at comma, semicolon, or unmatched closing delimiter
	lastPos := 0
	for {
		current := tok.Current()
		lastPos = current.ByteEnd()

		if current.Kind == tokenizer.EOF {
			return lastPos, nil
		}

		switch current.Kind {
		case tokenizer.LBRACE:
			braceDepth++
		case tokenizer.RBRACE:
			braceDepth--
			if braceDepth < 0 {
				return current.BytePos(), nil // Unmatched = end of lambda
			}
			if braceDepth == 0 {
				tok.Advance()
				return current.ByteEnd(), nil // Block lambda complete
			}
		case tokenizer.LPAREN:
			parenDepth++
		case tokenizer.RPAREN:
			parenDepth--
			if parenDepth < 0 {
				return current.BytePos(), nil // Unmatched paren = end
			}
		case tokenizer.LBRACKET:
			bracketDepth++
		case tokenizer.RBRACKET:
			bracketDepth--
			if bracketDepth < 0 {
				return current.BytePos(), nil
			}
		case tokenizer.COMMA, tokenizer.SEMICOLON:
			if braceDepth == 0 && parenDepth == 0 && bracketDepth == 0 {
				return current.BytePos(), nil // End of expression
			}
		case tokenizer.NEWLINE:
			// Newline ends expression-style lambda if not inside delimiters
			// Expression lambdas like: (x) => x + 1  should end at the newline
			// Block lambdas like: (x) => { ... } are handled by the RBRACE case
			if braceDepth == 0 && parenDepth == 0 && bracketDepth == 0 {
				return current.BytePos(), nil
			}
		}

		tok.Advance()
	}
}

// detectContext scans backward from expression start to determine context
func detectContext(src []byte, exprStart int) ExprContext {
	if exprStart == 0 {
		return ContextStatement
	}

	pos := exprStart - 1

	// Skip whitespace backward
	for pos >= 0 && (src[pos] == ' ' || src[pos] == '\t' || src[pos] == '\n' || src[pos] == '\r') {
		pos--
	}

	if pos < 0 {
		return ContextStatement
	}

	// Check for := or =
	if src[pos] == '=' {
		if pos > 0 && src[pos-1] == ':' {
			return ContextAssignment
		}
		return ContextAssignment
	}

	// Check for "return" keyword (look backward up to 6 chars)
	if pos >= 5 {
		// Look for 'n' of "return"
		end := pos + 1
		start := end - 6
		if start < 0 {
			start = 0
		}
		word := string(src[start:end])
		if len(word) >= 6 && word[len(word)-6:] == "return" {
			// Verify it's actually the keyword (check preceding char)
			if start == 0 || !isIdentifierChar(src[start-1]) {
				return ContextReturn
			}
		}
	}

	// Check for opening paren or comma (function argument)
	if src[pos] == '(' || src[pos] == ',' {
		return ContextArgument
	}

	return ContextStatement
}

// ContextInfo contains full context information for an expression (token-based)
type ContextInfo struct {
	Context        ExprContext
	StatementStart int
	StatementEnd   int
	VarName        string
}

// detectContextFromTokens uses tokens to find context and statement boundaries.
// This is the proper way to detect context - using tokenizer output, not byte scanning.
func detectContextFromTokens(tokens []tokenizer.Token, exprTokenIdx int, exprEnd int) ContextInfo {
	result := ContextInfo{
		Context:        ContextStatement,
		StatementStart: 0,
		StatementEnd:   exprEnd,
	}

	if exprTokenIdx <= 0 || len(tokens) == 0 {
		return result
	}

	// Scan backward to find context and statement start
	for i := exprTokenIdx - 1; i >= 0; i-- {
		tok := tokens[i]

		switch tok.Kind {
		case tokenizer.DEFINE: // :=
			result.Context = ContextAssignment
			// Find the variable name (should be the IDENT before :=)
			if i > 0 && tokens[i-1].Kind == tokenizer.IDENT {
				result.VarName = tokens[i-1].Lit
				// Statement starts at the identifier
				result.StatementStart = tokens[i-1].BytePos()
			}
			// Find statement end (use exprEnd if provided, otherwise search)
			if exprEnd > 0 {
				result.StatementEnd = exprEnd
			} else {
				result.StatementEnd = findStatementEndFromTokens(tokens, exprTokenIdx)
			}
			return result

		case tokenizer.ASSIGN: // =
			result.Context = ContextAssignment
			// Find the variable name (should be the IDENT before =)
			if i > 0 && tokens[i-1].Kind == tokenizer.IDENT {
				result.VarName = tokens[i-1].Lit
				result.StatementStart = tokens[i-1].BytePos()
			}
			// Find statement end (use exprEnd if provided, otherwise search)
			if exprEnd > 0 {
				result.StatementEnd = exprEnd
			} else {
				result.StatementEnd = findStatementEndFromTokens(tokens, exprTokenIdx)
			}
			return result

		case tokenizer.RETURN:
			result.Context = ContextReturn
			result.StatementStart = tok.BytePos()
			// Find statement end (use exprEnd if provided, otherwise search)
			if exprEnd > 0 {
				result.StatementEnd = exprEnd
			} else {
				result.StatementEnd = findStatementEndFromTokens(tokens, exprTokenIdx)
			}
			return result

		case tokenizer.LPAREN, tokenizer.COMMA:
			result.Context = ContextArgument
			// For argument context, find the start of the containing statement
			// Scan backward to find newline/semicolon/brace that starts the statement
			stmtStartIdx := findStatementStartFromTokens(tokens, i)
			if stmtStartIdx >= 0 && stmtStartIdx < len(tokens) {
				result.StatementStart = tokens[stmtStartIdx].BytePos()
			}
			result.StatementEnd = findStatementEndFromTokens(tokens, exprTokenIdx)
			return result

		case tokenizer.NEWLINE, tokenizer.SEMICOLON, tokenizer.LBRACE:
			// Hit statement boundary without finding context marker
			return result
		}
	}

	return result
}

// findStatementStartFromTokens scans backward to find the start of the current statement.
// Returns the token index that starts the statement, or 0 if not found.
func findStatementStartFromTokens(tokens []tokenizer.Token, fromIdx int) int {
	braceDepth := 0
	parenDepth := 0

	for i := fromIdx - 1; i >= 0; i-- {
		switch tokens[i].Kind {
		case tokenizer.RBRACE:
			braceDepth++
		case tokenizer.LBRACE:
			if braceDepth > 0 {
				braceDepth--
			} else {
				// This brace opens a block, statement starts after it
				return i + 1
			}
		case tokenizer.RPAREN:
			parenDepth++
		case tokenizer.LPAREN:
			if parenDepth > 0 {
				parenDepth--
			}
		case tokenizer.NEWLINE, tokenizer.SEMICOLON:
			// Only treat as statement boundary if we're not inside braces/parens
			if braceDepth == 0 && parenDepth == 0 {
				return i + 1 // Statement starts after the newline/semicolon
			}
		}
	}

	// Didn't find a boundary, but don't return 0 (would be package declaration)
	// Return the first non-trivial token after any leading stuff
	for i := 0; i < len(tokens); i++ {
		if tokens[i].Kind == tokenizer.IDENT && tokens[i].Lit == "func" {
			// Found a func keyword, this could be a statement start
			continue
		}
		if tokens[i].Kind == tokenizer.IDENT ||
			tokens[i].Kind == tokenizer.IF ||
			tokens[i].Kind == tokenizer.FOR ||
			tokens[i].Kind == tokenizer.RETURN {
			return i
		}
	}
	return 0
}

// findStatementEndFromTokens finds the end of a statement by scanning for NEWLINE or SEMICOLON.
// It tracks brace depth to skip newlines inside {} blocks (e.g., match expressions).
func findStatementEndFromTokens(tokens []tokenizer.Token, startIdx int) int {
	braceDepth := 0

	for i := startIdx; i < len(tokens); i++ {
		switch tokens[i].Kind {
		case tokenizer.LBRACE:
			braceDepth++
		case tokenizer.RBRACE:
			braceDepth--
			// If we just closed all braces, the next newline/semicolon ends the statement
			// Or if there's no more braces and we're at depth 0, the RBRACE itself might be the end
			if braceDepth < 0 {
				braceDepth = 0
			}
		case tokenizer.NEWLINE, tokenizer.SEMICOLON:
			// Only treat as statement end if we're not inside braces
			if braceDepth == 0 {
				return tokens[i].BytePos()
			}
		case tokenizer.EOF:
			if i > 0 {
				return tokens[i-1].ByteEnd()
			}
			return 0
		}
	}

	// Return end of last token if no terminator found
	if len(tokens) > 0 {
		return tokens[len(tokens)-1].ByteEnd()
	}
	return 0
}

// isIdentifierChar returns true if rune can be part of identifier
func isIdentifierChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_'
}

// findOperandStart scans backward from questionIdx to find where the operand expression starts
// It handles: foo(), x.bar(), arr[i], a.b.c(), x?.y, x?.y?.z, etc.
func findOperandStart(tokens []tokenizer.Token, questionIdx int, src []byte) int {
	pos, _ := findOperandStartWithIndex(tokens, questionIdx, src)
	return pos
}

// findOperandStartWithIndex scans backward from questionIdx to find where the operand expression starts.
// Returns both the byte position and the token index of the operand start.
// It handles: foo(), x.bar(), arr[i], a.b.c(), x?.y, x?.y?.z, etc.
func findOperandStartWithIndex(tokens []tokenizer.Token, questionIdx int, src []byte) (int, int) {
	if questionIdx == 0 {
		return 0, 0
	}

	// Walk backward, tracking balanced delimiters
	depth := 0
	start := questionIdx - 1

	for start >= 0 {
		tok := tokens[start]

		switch tok.Kind {
		case tokenizer.RPAREN, tokenizer.RBRACKET:
			depth++
		case tokenizer.LPAREN, tokenizer.LBRACKET:
			depth--
			if depth < 0 {
				return tokens[start+1].BytePos(), start + 1
			}
		case tokenizer.DOT, tokenizer.QUESTION_DOT:
			// Part of selector/safe nav chain, continue backward
		case tokenizer.IDENT:
			if depth == 0 {
				// Check if previous token continues the expression
				if start > 0 {
					prev := tokens[start-1]
					if prev.Kind == tokenizer.DOT || prev.Kind == tokenizer.QUESTION_DOT {
						start--
						continue
					}
				}
				return tok.BytePos(), start
			}
		case tokenizer.ASSIGN, tokenizer.DEFINE, tokenizer.COMMA,
			tokenizer.SEMICOLON, tokenizer.LBRACE, tokenizer.RETURN:
			// Statement boundary - operand starts at next token
			if start+1 < len(tokens) {
				return tokens[start+1].BytePos(), start + 1
			}
			return tok.ByteEnd(), start
		}
		start--
	}

	if len(tokens) > 0 {
		return tokens[0].BytePos(), 0
	}
	return 0, 0
}

// findNullCoalesceEnd finds the end of the right side of a null coalesce expression
// Handles: a ?? b, a ?? b ?? c (left associative), a ?? (b + c), etc.
func findNullCoalesceEnd(tok *tokenizer.Tokenizer, allTokens []tokenizer.Token, startIdx int) int {
	braceDepth := 0
	parenDepth := 0
	bracketDepth := 0
	lastPos := 0

	for {
		current := tok.Current()
		if current.Kind == tokenizer.EOF {
			return lastPos
		}

		lastPos = current.ByteEnd()

		switch current.Kind {
		case tokenizer.LBRACE:
			braceDepth++
		case tokenizer.RBRACE:
			braceDepth--
			if braceDepth < 0 {
				return current.BytePos() // Unmatched = end of expression
			}
		case tokenizer.LPAREN:
			parenDepth++
		case tokenizer.RPAREN:
			parenDepth--
			if parenDepth < 0 {
				return current.BytePos() // Unmatched = end of expression
			}
		case tokenizer.LBRACKET:
			bracketDepth++
		case tokenizer.RBRACKET:
			bracketDepth--
			if bracketDepth < 0 {
				return current.BytePos()
			}
		case tokenizer.COMMA, tokenizer.SEMICOLON:
			if braceDepth == 0 && parenDepth == 0 && bracketDepth == 0 {
				return current.BytePos() // Statement terminator at depth 0
			}
		case tokenizer.QUESTION_QUESTION:
			// Another ?? - this is a chained coalesce, continue scanning
			// a ?? b ?? c should be one complete expression
			// Don't return here - let it continue to find the full chain
		case tokenizer.NEWLINE:
			// Newline at depth 0 ends the expression
			if braceDepth == 0 && parenDepth == 0 && bracketDepth == 0 {
				return current.BytePos()
			}
		case tokenizer.IDENT, tokenizer.STRING, tokenizer.INT, tokenizer.FLOAT:
			// These are valid right operands - we want to consume them
			// and then check if the next token continues the expression
			tok.Advance()

			// Check if next token continues the expression
			next := tok.Current()
			if braceDepth == 0 && parenDepth == 0 && bracketDepth == 0 {
				// If next is not a selector/call/index/another ??, we're done
				if next.Kind != tokenizer.DOT && next.Kind != tokenizer.LPAREN &&
					next.Kind != tokenizer.LBRACKET && next.Kind != tokenizer.QUESTION_QUESTION {
					return lastPos
				}
			}
			continue
		}

		tok.Advance()
	}
}

// findSafeNavEnd finds the end of a safe navigation chain
// Handles: x?.y, x?.y.z, x?.y?.z, x?.foo(), x?.y[0], etc.
// CRITICAL: If followed by ??, returns -1 to signal that the entire expression
// should be detected as a null coalesce instead (since ?? has lower precedence)
func findSafeNavEnd(tok *tokenizer.Tokenizer, allTokens []tokenizer.Token, startIdx int) int {
	braceDepth := 0
	parenDepth := 0
	bracketDepth := 0
	lastPos := 0

	for {
		current := tok.Current()
		if current.Kind == tokenizer.EOF {
			return lastPos
		}

		lastPos = current.ByteEnd()

		switch current.Kind {
		case tokenizer.LBRACE:
			braceDepth++
		case tokenizer.RBRACE:
			braceDepth--
			if braceDepth < 0 {
				return current.BytePos()
			}
		case tokenizer.LPAREN:
			parenDepth++
		case tokenizer.RPAREN:
			parenDepth--
			if parenDepth < 0 {
				return current.BytePos()
			}
			// If we just closed a call and depth is 0, continue to check for more chaining
		case tokenizer.LBRACKET:
			bracketDepth++
		case tokenizer.RBRACKET:
			bracketDepth--
			if bracketDepth < 0 {
				return current.BytePos()
			}
			// If we just closed an index and depth is 0, continue to check for more chaining
		case tokenizer.DOT, tokenizer.QUESTION_DOT:
			// Continue - part of the chain
		case tokenizer.IDENT:
			// Continue - field name or method name
		case tokenizer.QUESTION_QUESTION:
			// If followed by ??, the whole expression is a null coalesce
			// Return -1 to signal that we should skip this detection
			// (null coalesce finder will handle the entire expression)
			if braceDepth == 0 && parenDepth == 0 && bracketDepth == 0 {
				return -1
			}
		case tokenizer.COMMA, tokenizer.SEMICOLON:
			if braceDepth == 0 && parenDepth == 0 && bracketDepth == 0 {
				return current.BytePos()
			}
		case tokenizer.NEWLINE:
			if braceDepth == 0 && parenDepth == 0 && bracketDepth == 0 {
				next := tok.PeekToken()
				// If next token is not a continuation (dot or ?.), end here
				if next.Kind != tokenizer.DOT && next.Kind != tokenizer.QUESTION_DOT {
					return current.BytePos()
				}
			}
		default:
			// Unknown token at depth 0 - likely end of expression
			if braceDepth == 0 && parenDepth == 0 && bracketDepth == 0 {
				// Check if this is an operator or other construct
				if current.Kind != tokenizer.DOT && current.Kind != tokenizer.QUESTION_DOT {
					return current.BytePos()
				}
			}
		}

		tok.Advance()
	}
}

// findMatchingColon looks ahead from questionIdx to find a : token that matches this ? for ternary
// Returns the token index of the matching :, or -1 if not found
func findMatchingColon(tokens []tokenizer.Token, questionIdx int) int {
	if questionIdx >= len(tokens)-1 {
		return -1
	}

	// Track nesting depth to handle nested ternaries and other structures
	depth := 0
	parenDepth := 0
	braceDepth := 0
	bracketDepth := 0

	for i := questionIdx + 1; i < len(tokens); i++ {
		tok := tokens[i]

		switch tok.Kind {
		case tokenizer.LPAREN:
			parenDepth++
		case tokenizer.RPAREN:
			parenDepth--
		case tokenizer.LBRACE:
			braceDepth++
		case tokenizer.RBRACE:
			braceDepth--
			// If we hit } at depth 0, we're leaving the statement
			if braceDepth < 0 {
				return -1
			}
		case tokenizer.LBRACKET:
			bracketDepth++
		case tokenizer.RBRACKET:
			bracketDepth--
		case tokenizer.QUESTION:
			// Nested ternary - increase depth
			if i+1 < len(tokens) {
				next := tokens[i+1]
				if next.Kind != tokenizer.QUESTION && next.Kind != tokenizer.DOT {
					depth++
				}
			}
		case tokenizer.COLON:
			// Check if this colon is at our depth level
			if depth == 0 && parenDepth == 0 && braceDepth == 0 && bracketDepth == 0 {
				// Found the matching colon!
				return i
			}
			// If depth > 0, this belongs to a nested ternary
			if depth > 0 {
				depth--
			} else {
				// Colon in wrong context (like case:, struct field, etc.)
				return -1
			}
		case tokenizer.SEMICOLON, tokenizer.COMMA:
			// Statement/argument terminators at depth 0 mean no matching colon
			if depth == 0 && parenDepth == 0 && braceDepth == 0 && bracketDepth == 0 {
				return -1
			}
		case tokenizer.NEWLINE, tokenizer.COMMENT:
			// Skip newlines and comments - ternaries can be multi-line
			continue
		case tokenizer.EOF:
			return -1
		}
	}

	return -1
}

// findTernaryCondStart scans backward from questionIdx to find where the condition starts
func findTernaryCondStart(tokens []tokenizer.Token, questionIdx int, src []byte) int {
	if questionIdx == 0 {
		return 0
	}

	// Walk backward, tracking balanced delimiters
	depth := 0
	start := questionIdx - 1

	for start >= 0 {
		tok := tokens[start]

		switch tok.Kind {
		case tokenizer.RPAREN, tokenizer.RBRACKET:
			depth++
		case tokenizer.LPAREN, tokenizer.LBRACKET:
			depth--
			if depth < 0 {
				return tokens[start+1].BytePos()
			}
			// When depth reaches 0 after closing a group, the expression starts at this paren
			// Continue scanning to see if there's more (e.g., operators before the paren)
		case tokenizer.DOT, tokenizer.QUESTION_DOT:
			// Part of chain, continue backward
		case tokenizer.IDENT, tokenizer.INT, tokenizer.FLOAT, tokenizer.STRING, tokenizer.CHAR, tokenizer.TRUE, tokenizer.FALSE, tokenizer.NIL:
			// Keep scanning if at depth > 0
			if depth == 0 {
				// Check if previous token suggests this is part of larger expr
				if start > 0 {
					prev := tokens[start-1]
					if prev.Kind != tokenizer.DOT && prev.Kind != tokenizer.QUESTION_DOT &&
						!isOperator(prev.Kind) && prev.Kind != tokenizer.LPAREN {
						return tok.BytePos()
					}
				} else {
					return tok.BytePos()
				}
			}
		case tokenizer.RETURN, tokenizer.IF, tokenizer.FOR, tokenizer.SWITCH, tokenizer.CASE,
			tokenizer.VAR, tokenizer.CONST, tokenizer.TYPE, tokenizer.FUNC, tokenizer.LET,
			tokenizer.ASSIGN, tokenizer.DEFINE:
			// Statement-starting keywords and assignment operators are boundaries
			if start+1 < len(tokens) {
				return tokens[start+1].BytePos()
			}
			return tok.ByteEnd()
		default:
			// At depth 0, most other tokens end the backward scan
			if depth == 0 {
				// Binary operators are part of the condition
				if isOperator(tok.Kind) {
					// Continue scanning
				} else if isBoundary(tok.Kind) {
					return tokens[start+1].BytePos()
				}
			}
		}

		start--
	}

	// Reached beginning
	if start < 0 && len(tokens) > 0 {
		return tokens[0].BytePos()
	}
	return 0
}

// findTernaryFalseEnd scans forward from colonIdx to find where the false expression ends
func findTernaryFalseEnd(tokens []tokenizer.Token, colonIdx int, src []byte) int {
	if colonIdx >= len(tokens)-1 {
		return tokens[colonIdx].ByteEnd()
	}

	// Walk forward, tracking balanced delimiters and nested ternaries
	depth := 0
	ternaryDepth := 0 // Track nested ternary depth
	i := colonIdx + 1

	for i < len(tokens) {
		tok := tokens[i]

		switch tok.Kind {
		case tokenizer.LPAREN, tokenizer.LBRACKET, tokenizer.LBRACE:
			depth++
		case tokenizer.RPAREN, tokenizer.RBRACKET:
			depth--
			if depth < 0 {
				return tokens[i-1].ByteEnd()
			}
		case tokenizer.RBRACE:
			depth--
			if depth < 0 {
				return tokens[i-1].ByteEnd()
			}
		case tokenizer.QUESTION:
			// Check if this is a ternary ? (not ?? or ?.)
			if i+1 < len(tokens) {
				next := tokens[i+1]
				if next.Kind != tokenizer.QUESTION && next.Kind != tokenizer.DOT {
					// Nested ternary - increase depth
					ternaryDepth++
				}
			}
		case tokenizer.COLON:
			// Check if this colon belongs to a nested ternary or is a boundary
			if ternaryDepth > 0 {
				// This colon closes a nested ternary
				ternaryDepth--
			} else if depth == 0 {
				// Colon at depth 0 and no nested ternary - could be case label or struct tag
				// This ends our expression
				return tokens[i-1].ByteEnd()
			}
		case tokenizer.SEMICOLON, tokenizer.COMMA:
			if depth == 0 && ternaryDepth == 0 {
				return tokens[i-1].ByteEnd()
			}
		case tokenizer.EOF:
			if i > 0 {
				return tokens[i-1].ByteEnd()
			}
			return tok.BytePos()
		case tokenizer.NEWLINE:
			// Newlines can appear in multi-line ternaries
			// At depth 0, check if next token continues the expression
			if depth == 0 && ternaryDepth == 0 {
				// Look ahead to see if the expression continues after newline
				if i+1 < len(tokens) {
					nextTok := tokens[i+1]
					// Keywords like let, return, func, var, const, type, etc. start new statements
					if isStatementStartKeyword(nextTok.Kind) {
						return tokens[i-1].ByteEnd()
					}
					// If next token can start/continue an expression, check more carefully
					if isExprToken(nextTok.Kind) || isOperator(nextTok.Kind) {
						// For IDENT specifically, need to check if it looks like a new statement
						// e.g., "let x = ..." after a ternary should NOT be included
						if nextTok.Kind == tokenizer.IDENT {
							// If we see IDENT followed by := or =, it's a new statement
							if i+2 < len(tokens) {
								afterIdent := tokens[i+2]
								if afterIdent.Kind == tokenizer.DEFINE || afterIdent.Kind == tokenizer.ASSIGN {
									return tokens[i-1].ByteEnd()
								}
							}
						}
						// Continue scanning - expression continues
					} else {
						// Next token doesn't continue expression - terminate here
						return tokens[i-1].ByteEnd()
					}
				} else {
					// No more tokens after newline
					return tokens[i-1].ByteEnd()
				}
			}
			// If inside delimiters or nested ternary, newline doesn't terminate
		case tokenizer.COMMENT:
			// Skip comments - ternaries can have comments
			continue
		default:
			// For non-delimiters, check if we're still in expression
			if depth == 0 && ternaryDepth == 0 {
				// Check for expression terminators
				if isBoundary(tok.Kind) {
					if i > colonIdx+1 {
						return tokens[i-1].ByteEnd()
					}
				}
			}
		}

		i++
	}

	// End of tokens
	if i > 0 && i < len(tokens) {
		return tokens[i-1].ByteEnd()
	} else if len(tokens) > colonIdx {
		return tokens[len(tokens)-1].ByteEnd()
	}
	return 0
}

// findTokenIdxForBytePos finds the token index for a given byte position
func findTokenIdxForBytePos(tokens []tokenizer.Token, bytePos int) int {
	for i, t := range tokens {
		if t.BytePos() == bytePos {
			return i
		}
		if t.BytePos() > bytePos {
			if i > 0 {
				return i - 1
			}
			return 0
		}
	}
	if len(tokens) > 0 {
		return len(tokens) - 1
	}
	return 0
}

// isOperator returns true if token is a binary operator
func isOperator(kind tokenizer.TokenKind) bool {
	switch kind {
	case tokenizer.PLUS, tokenizer.MINUS, tokenizer.STAR, tokenizer.SLASH,
		tokenizer.AND, tokenizer.OR, tokenizer.EQ,
		tokenizer.NE, tokenizer.LT, tokenizer.GT, tokenizer.LE, tokenizer.GE,
		tokenizer.QUESTION_QUESTION:
		return true
	}
	return false
}

// isBoundary returns true if token marks a boundary that ends an expression
func isBoundary(kind tokenizer.TokenKind) bool {
	switch kind {
	case tokenizer.SEMICOLON, tokenizer.COMMA, tokenizer.LBRACE, tokenizer.RBRACE,
		tokenizer.COLON, tokenizer.EOF:
		return true
	}
	return false
}

// isExprToken returns true if the token can be part of an expression
func isExprToken(kind tokenizer.TokenKind) bool {
	switch kind {
	case tokenizer.IDENT, tokenizer.INT, tokenizer.FLOAT, tokenizer.STRING, tokenizer.CHAR,
		tokenizer.TRUE, tokenizer.FALSE, tokenizer.NIL,
		tokenizer.LPAREN, tokenizer.LBRACKET, tokenizer.LBRACE,
		tokenizer.NOT, tokenizer.MINUS, tokenizer.PLUS,
		tokenizer.QUESTION:
		return true
	}
	return false
}

// isStatementStartKeyword returns true if the token starts a new statement
func isStatementStartKeyword(kind tokenizer.TokenKind) bool {
	switch kind {
	case tokenizer.LET, tokenizer.VAR, tokenizer.CONST, tokenizer.TYPE,
		tokenizer.FUNC, tokenizer.RETURN, tokenizer.IF, tokenizer.FOR,
		tokenizer.SWITCH, tokenizer.PACKAGE, tokenizer.IMPORT:
		return true
	}
	return false
}

// isRustLambda checks if the current PIPE token starts a Rust-style lambda |params| body
// rather than being a bitwise OR operator. It looks ahead to find a closing | before
// any statement terminator. The tokenizer should be positioned at the opening |.
//
// Heuristic: between the opening and closing `|`, only tokens that can appear in a
// lambda parameter list are allowed. Anything else (a literal, an arithmetic
// operator, a `.`, a `*` outside a type position …) means this is a bitwise-or
// chain, not a lambda. Without this check, expressions like `A|B|C` are
// misinterpreted as `A` followed by `|B|` (a lambda taking parameter `B`) — see
// pkg/transpiler/bitwise_or_test.go.
func isRustLambda(tok *tokenizer.Tokenizer, allTokens []tokenizer.Token) bool {
	// Find current position in allTokens
	currentPos := tok.Current().Pos
	startIdx := -1
	for i, t := range allTokens {
		if t.Pos == currentPos {
			startIdx = i
			break
		}
	}
	if startIdx < 0 {
		return false
	}

	// Look back at the previous significant token. If it can produce a
	// value (an identifier, a literal, or a closing bracket), then this
	// PIPE is the binary bitwise-or operator, not the opener of a lambda.
	// A lambda only opens where a value would be expected — after `=`,
	// `:=`, `(`, `,`, `return`, etc., or at the very start of an
	// expression. This guards against false positives in chains like
	// `a|b|c|d` where the finder revisits each `|` separately.
	for j := startIdx - 1; j >= 0; j-- {
		switch allTokens[j].Kind {
		case tokenizer.NEWLINE, tokenizer.SEMICOLON:
			// Skip insignificant whitespace/separator tokens.
			continue
		case tokenizer.IDENT, tokenizer.INT, tokenizer.FLOAT, tokenizer.STRING,
			tokenizer.CHAR, tokenizer.TRUE, tokenizer.FALSE, tokenizer.NIL,
			tokenizer.RPAREN, tokenizer.RBRACKET, tokenizer.RBRACE,
			tokenizer.UNDERSCORE:
			// Value-producing previous token → this `|` is bitwise-or.
			return false
		}
		break
	}

	// Scan forward looking for closing | before statement terminator.
	//
	// We model the lambda parameter list as a small state machine. Each
	// parameter entry is `IDENT[: Type]`, separated by commas. The name
	// slot accepts only an identifier or `_`; the type slot accepts the
	// usual type-position tokens (`pkg.T`, `*T`, `[]T`, `map[K]V`, etc.).
	// If we see something that fits neither slot — say, a DOT while we
	// still expect a parameter name, or a literal anywhere — we know this
	// PIPE is bitwise-or, not a lambda opener.
	const (
		paramSlotName = iota // waiting for an IDENT or `_` (start of an entry)
		paramSlotAfterName   // saw a name, waiting for `:`, `,`, or closing `|`
		paramSlotType        // after `:`, scanning the type expression
	)
	parenDepth := 0
	slot := paramSlotName
	for i := startIdx + 1; i < len(allTokens); i++ {
		t := allTokens[i]

		switch t.Kind {
		case tokenizer.PIPE:
			if parenDepth > 0 {
				return false
			}
			// Closing | only counts as a lambda if we are *between* params:
			// after the name slot was filled (paramSlotAfterName) or after
			// a type was scanned (paramSlotType). If we are still expecting
			// a name (e.g. immediately after the opening |), the empty
			// param list `||` is also a valid lambda — handle that too.
			if slot == paramSlotName && i == startIdx+1 {
				return true // empty params: `||`
			}
			return slot != paramSlotName

		case tokenizer.LPAREN, tokenizer.LBRACKET:
			// Allow `(...)` and `[...]` inside the type slot of a param
			// (e.g. `|x: map[K]V|` or `|x: (int, int)|`). Outside a type
			// slot they cannot legally appear in a param list.
			if slot != paramSlotType {
				return false
			}
			parenDepth++

		case tokenizer.RPAREN, tokenizer.RBRACKET:
			parenDepth--
			if parenDepth < 0 {
				return false
			}

		case tokenizer.IDENT:
			switch slot {
			case paramSlotName:
				slot = paramSlotAfterName
			case paramSlotAfterName:
				// Go-style `x Type` (after TransformSource the colon is
				// gone) — accept and transition into the type slot.
				slot = paramSlotType
			case paramSlotType:
				// Already inside a type expression; nested ident is fine.
			}

		case tokenizer.UNDERSCORE:
			if slot != paramSlotName {
				return false
			}
			slot = paramSlotAfterName

		case tokenizer.COMMA:
			if parenDepth == 0 {
				if slot == paramSlotName {
					return false
				}
				slot = paramSlotName
			}

		case tokenizer.COLON:
			if slot != paramSlotAfterName {
				return false
			}
			slot = paramSlotType

		case tokenizer.DOT:
			// Qualified type name like `pkg.T` — only valid in type slot.
			if slot != paramSlotType {
				return false
			}

		case tokenizer.STAR:
			// Pointer type `*T` — only valid at the start of a type slot.
			if slot != paramSlotType {
				return false
			}

		case tokenizer.SEMICOLON, tokenizer.LBRACE, tokenizer.RBRACE, tokenizer.EOF:
			// Statement terminator before finding closing | - this is bitwise OR
			return false

		case tokenizer.NEWLINE:
			// Newline at depth 0 terminates - this is bitwise OR
			if parenDepth == 0 {
				return false
			}

		case tokenizer.ASSIGN, tokenizer.DEFINE:
			// Assignment operators - not valid in lambda params
			return false

		case tokenizer.INT, tokenizer.FLOAT, tokenizer.STRING, tokenizer.CHAR,
			tokenizer.TRUE, tokenizer.FALSE, tokenizer.NIL:
			// A literal between the two pipes cannot be a parameter name
			// or a type — this is a bitwise-or chain like `1 | 2 | 4`.
			return false

		default:
			// For other tokens (operators, etc.), if we're not inside parens
			// and it's a binary operator, this is likely bitwise OR context
			if parenDepth == 0 && isBinaryOperator(t.Kind) {
				return false
			}
		}
	}

	// Reached end without finding closing | - not a lambda
	return false
}

// isBinaryOperator returns true if the token is a binary operator
// that would indicate we're in an expression context, not lambda params
func isBinaryOperator(kind tokenizer.TokenKind) bool {
	switch kind {
	case tokenizer.PLUS, tokenizer.MINUS, tokenizer.STAR, tokenizer.SLASH,
		tokenizer.AND, tokenizer.OR, tokenizer.EQ, tokenizer.NE,
		tokenizer.LT, tokenizer.GT, tokenizer.LE, tokenizer.GE,
		tokenizer.QUESTION_QUESTION, tokenizer.QUESTION, tokenizer.QUESTION_DOT:
		return true
	}
	return false
}
