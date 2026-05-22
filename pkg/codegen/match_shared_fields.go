package codegen

import (
	"fmt"
	"go/scanner"
	"go/token"
	"strings"

	"github.com/MadAppGang/dingo/pkg/ast"
)

// match_shared_fields.go — match codegen for the Option B (shared-fields)
// enum layout.
//
// Background: when an enum is declared with a `shared { ... }` block, the
// codegen in pkg/ast/enum_codegen.go lowers it to a *struct* (not an
// interface). The struct carries the shared fields directly, plus a `tag`
// (typed `<Enum>Tag uint8`) and a `data` field of sealed-interface type
// `<Enum>VariantData`. Each variant has a thin `<Enum><Variant>Data`
// struct holding only its variant-specific fields.
//
// Because the enum is no longer an interface, the classic match codegen
// (`switch v := scrutinee.(type)`) does not apply. Instead this file
// emits `switch scrut.tag { case <Enum>Tag<Variant>: ... }` and, for arms
// that bind variant-specific fields, an interface assertion on
// `scrut.data` to recover the typed Data struct.

// detectSharedFieldsEnum returns the enum name backing this match if it
// targets a shared-fields (Option B) enum, or "" otherwise.
//
// We resolve the enum from any constructor pattern in the arms (a single
// match cannot mix two different enums under the current type system).
func (g *MatchCodeGen) detectSharedFieldsEnum() string {
	if g.Match == nil || len(g.Match.Arms) == 0 {
		return ""
	}
	if g.Context == nil || g.Context.ValueEnumReg == nil {
		return ""
	}
	reg := g.Context.ValueEnumReg

	for _, arm := range g.Match.Arms {
		cp, ok := arm.Pattern.(*ast.ConstructorPattern)
		if !ok {
			continue
		}
		enumName, ok := reg.IsSumTypeVariant(cp.Name)
		if !ok {
			// Could be Status_Pending style — strip the prefix and retry.
			// We use the same naming convention as constructorToTypeName.
			continue
		}
		if reg.IsSharedFieldsEnum(enumName) {
			return enumName
		}
		return ""
	}
	return ""
}

// generateSharedFieldsMatch emits a tag-dispatch switch for a shared-fields
// enum. Pattern bindings extract fields from the variant Data struct via
// a single interface assertion (`d := scrut.data.(*<Enum><Variant>Data)`),
// after which `d.X` reads the variant-specific field directly.
//
// Shared fields (`pos`, `op`, ...) are NOT accessed via the Data struct —
// the user references them through the original scrutinee variable, which
// is still a `*<Enum>` and carries the shared fields on its top-level
// struct. We make no rewrite of arm bodies for that case.
func (g *MatchCodeGen) generateSharedFieldsMatch(enumName string) ast.CodeGenResult {
	// Exhaustiveness check piggybacks on the existing infrastructure —
	// the enum's variants are registered in the registry the same way
	// as classic enums.
	if g.Match.IsExpr {
		if errResult := g.checkExhaustiveness(); errResult.Error != nil {
			return errResult
		}
	}

	// Statement context: emit switch directly. Expression context:
	// emit it inside an IIFE so the match has a value. Return context:
	// emit switch with direct returns (matching the existing pattern
	// used for the classic layout).
	isReturnCtx := g.Match.IsExpr && g.Context != nil && g.Context.Context == ast.ContextReturn

	if g.Match.IsExpr && !isReturnCtx {
		g.Write("func() interface{} {\n")
	}

	g.emitSharedFieldsSwitch(enumName)

	if g.Match.IsExpr && !isReturnCtx {
		g.Write("\n}()")
	}

	// For return context, hand the switch over as a statement-level
	// replacement (same convention as the classic layout).
	result := g.Result()
	if isReturnCtx {
		result.StatementOutput = result.Output
		result.Output = nil
	}
	return result
}

// emitSharedFieldsSwitch is the core of the shared-fields match codegen:
// it writes `scrut := <scrutinee>` then `switch scrut.tag { ... }` with
// one case per arm.
func (g *MatchCodeGen) emitSharedFieldsSwitch(enumName string) {
	// Capture scrutinee once to avoid double evaluation of side-effectful
	// expressions (function calls, etc.). Same precaution as the classic
	// path.
	scrutineeResult := GenerateExpr(g.Match.Scrutinee)
	scrutVar := g.SharedTempVar("scrut")
	g.Write(fmt.Sprintf("%s := %s\n", scrutVar, string(scrutineeResult.Output)))

	// Cross-package enum: when the matched enum lives in another package,
	// every <Enum>Tag<Variant> / *<Enum><Variant>Data identifier we emit
	// must be qualified with the package alias. pkgPrefix is "" for a
	// local enum and "<alias>." when the enum was discovered via the
	// import scan.
	pkgPrefix := ""
	if g.Context != nil && g.Context.ValueEnumReg != nil {
		if pkg := g.Context.ValueEnumReg.PackageOf(enumName); pkg != "" {
			pkgPrefix = pkg + "."
		}
	}
	// scrut.tag is a lowercase unexported field on the enum struct, so
	// it isn't accessible from outside the home package. For cross-package
	// matches, fall back to the exported Tag() accessor.
	tagAccess := ".tag"
	if pkgPrefix != "" {
		tagAccess = ".Tag()"
	}
	g.Write(fmt.Sprintf("switch %s%s {\n", scrutVar, tagAccess))

	for _, arm := range g.Match.Arms {
		g.emitSharedFieldsArm(enumName, pkgPrefix, scrutVar, arm)
	}

	g.Write("}")

	// Return context: Go's control flow analysis requires a guaranteed
	// return after an exhaustive switch. The exhaustiveness checker has
	// already verified all cases are covered, so this panic is dead code,
	// but Go's compiler can't prove that.
	if g.Match.IsExpr && g.Context != nil && g.Context.Context == ast.ContextReturn {
		g.Write("\npanic(\"unreachable: exhaustive match\")")
	}
}

// refBinding describes a `&X`-pattern binding: the user-facing name X and
// the Go expression that refers in-place to the underlying variant field.
// emitArmBody substitutes the name with the field-access expression in the
// arm body so that `X = ...` writes through to the variant data.
type refBinding struct {
	name        string // binding identifier as written in the pattern
	fieldAccess string // e.g. "d1.X" — token-substituted into the body
}

// emitSharedFieldsArm writes one `case` clause (or `default`) for a single
// match arm. pkgPrefix is "" for a local enum and "<alias>." for a
// cross-package enum (so the emitted Go references are qualified).
func (g *MatchCodeGen) emitSharedFieldsArm(enumName, pkgPrefix, scrutVar string, arm *ast.MatchArm) {
	switch pat := arm.Pattern.(type) {
	case *ast.ConstructorPattern:
		g.Write(fmt.Sprintf("case %s%sTag%s:\n", pkgPrefix, enumName, pat.Name))
		refs := g.emitSharedFieldsBindings(enumName, pkgPrefix, scrutVar, pat)
		g.emitArmBody(arm, refs)

	case *ast.WildcardPattern:
		g.Write("default:\n")
		g.emitArmBody(arm, nil)

	case *ast.VariablePattern:
		// `x => ...` rebinds the whole scrutinee. With shared-fields
		// enums the scrutinee is *<Enum>, so this is just an alias.
		g.Write("default:\n")
		g.Write(fmt.Sprintf("%s := %s\n", pat.Name, scrutVar))
		_ = pat
		g.emitArmBody(arm, nil)

	case *ast.LiteralPattern:
		// Literals don't make sense for a sum-type tag dispatch.
		// Emit a TODO comment rather than silently dropping the arm.
		g.Write(fmt.Sprintf("// dingo: literal pattern %q not supported in shared-fields match\n", pat.Value))

	default:
		g.Write("// dingo: unsupported pattern in shared-fields match\n")
	}
}

// emitSharedFieldsBindings writes the data-struct cast and per-field
// extraction lines for the bindings in a constructor pattern.
//
// For an arm like `AddrExpr(x, p)` the output is:
//
//	d_scrut := scrut.data.(*ExprAddrExprData)
//	x := d_scrut.X
//	p := d_scrut.Prealloc
//
// If the pattern has no bindings, only `_ = scrut.data` is emitted so
// the unused-var lint is satisfied (and a future-proofing in case the
// arm body needs the data — currently we just skip).
func (g *MatchCodeGen) emitSharedFieldsBindings(enumName, pkgPrefix, scrutVar string, pat *ast.ConstructorPattern) []refBinding {
	if len(pat.Params) == 0 {
		return nil
	}

	// patternBinding extracts the binding name AND its ByRef flag for a
	// single param in a constructor pattern. Both VariablePattern and
	// *nullary* ConstructorPattern act as named bindings — the latter
	// happens when the Pratt parser treats an uppercase identifier inside
	// a constructor pattern's params as a nullary constructor (its
	// uppercase-heuristic in isNullaryConstructor). For matching named-
	// struct variants, that identifier IS just the (uppercase) binding
	// name. Wildcards and literals don't bind.
	type binding struct {
		name  string
		byRef bool
	}
	patternBinding := func(p ast.Pattern) (binding, bool) {
		switch v := p.(type) {
		case *ast.VariablePattern:
			return binding{name: v.Name, byRef: v.ByRef}, true
		case *ast.ConstructorPattern:
			if len(v.Params) == 0 {
				return binding{name: v.Name, byRef: v.ByRef}, true
			}
		}
		return binding{}, false
	}

	// Determine which params actually produce bindings.
	hasBinding := false
	for _, p := range pat.Params {
		if _, ok := patternBinding(p); ok {
			hasBinding = true
			break
		}
	}
	if !hasBinding {
		return nil
	}

	// Emit the cast. For a local enum we cast scrut.data directly (`data`
	// is an unexported field, OK in the home package). For a cross-package
	// match we use the exported As<Variant>() accessor instead — it
	// validates the tag and returns *<Enum><Variant>Data without touching
	// the unexported `data` field.
	dataVar := g.SharedTempVar("d")
	if pkgPrefix != "" {
		g.Write(fmt.Sprintf("%s := %s.As%s()\n", dataVar, scrutVar, pat.Name))
	} else {
		dataStruct := fmt.Sprintf("*%s%sData", enumName, pat.Name)
		g.Write(fmt.Sprintf("%s := %s.data.(%s)\n", dataVar, scrutVar, dataStruct))
	}

	// Bindings:
	//
	//   - By-VALUE (default): emit `<name> := <dataVar>.<FieldName>` so the
	//     arm body sees a local copy. Assignment to <name> only writes the
	//     local — the variant data is untouched.
	//   - By-REFERENCE (`&<name>`): do NOT emit a binding line. Instead,
	//     collect the (name → fieldAccess) pair so emitArmBody can
	//     token-substitute the identifier in the arm body to refer to
	//     `<dataVar>.<FieldName>` directly. That way `X = expr` in the body
	//     becomes `<dataVar>.X = expr` in the generated Go, mutating the
	//     variant data in place.
	var refs []refBinding
	for _, param := range pat.Params {
		b, ok := patternBinding(param)
		if !ok {
			continue
		}
		// Tuple variants use Value/ValueN naming; we don't yet have
		// metadata here to detect that. Default to the binding name
		// (struct-variant convention), which is what Option B enums
		// will produce in practice.
		fieldAccess := fmt.Sprintf("%s.%s", dataVar, b.name)
		if b.byRef {
			refs = append(refs, refBinding{name: b.name, fieldAccess: fieldAccess})
			continue
		}
		g.Write(fmt.Sprintf("%s := %s\n", b.name, fieldAccess))
	}
	if len(refs) > 0 {
		// Suppress the unused-var lint on dataVar when ONLY by-ref bindings
		// are present — by-value bindings already reference dataVar, but
		// the substituted body might not retain the original token form
		// for the linter to count. Cheap insurance:
		g.Write(fmt.Sprintf("_ = %s\n", dataVar))
	}
	return refs
}

// isTerminatingExpr reports whether the generated Go expression is one of
// Go's terminating builtins/keywords — `panic(...)` and the obsolete `goto`
// — which never produce a value and cannot legally appear in a `return X`
// context. Such expressions must be emitted as bare statements instead.
//
// Conservative: only matches the literal prefix `panic(`. Wrapping the
// panic in any extra expression (e.g. `foo(panic(...))`) is treated as a
// normal value-producing call. That's the right call because Go's static
// type system already rejects those.
func isTerminatingExpr(goExprSrc string) bool {
	s := strings.TrimSpace(goExprSrc)
	return strings.HasPrefix(s, "panic(")
}

// emitArmBody writes the body of an arm. Mirrors the classic codegen's
// per-arm body emission: expression matches return the body; statement
// matches inline it. Guards are wrapped in an if-block. Re-implemented
// here (rather than calling generateArmBody) to avoid relying on the
// `scrutineeTempVar` / `Binding` plumbing used by the type-switch path.
//
// refs is the list of by-reference bindings collected from the arm's
// pattern. For each ref binding the function token-substitutes the
// binding identifier with the underlying field-access expression
// (e.g. `d1.X`) before emitting the body, so user-written `X = expr`
// becomes `d1.X = expr` in the generated Go and mutates the variant
// data in place.
func (g *MatchCodeGen) emitArmBody(arm *ast.MatchArm, refs []refBinding) {
	if arm.Guard != nil {
		guard := GenerateExpr(arm.Guard)
		g.Write(fmt.Sprintf("if %s {\n", string(guard.Output)))
	}

	body := GenerateExpr(arm.Body)
	output := body.Output
	for _, r := range refs {
		output = substituteIdentBytes(output, r.name, r.fieldAccess)
	}
	_, isReturnExpr := arm.Body.(*ast.ReturnExpr)
	bodyStr := string(output)
	if g.Match.IsExpr && !isReturnExpr {
		// `panic(...)` terminates the function and has type bottom; Go does
		// not allow `return panic(...)`. Emit the panic as a bare statement
		// instead — control never falls through.
		if isTerminatingExpr(bodyStr) {
			g.Buf.Write(output)
			g.WriteByte('\n')
		} else {
			g.Write(fmt.Sprintf("return %s\n", bodyStr))
		}
	} else {
		g.Buf.Write(output)
		g.WriteByte('\n')
	}

	if arm.Guard != nil {
		g.Write("}\n")
	}
}

// substituteIdentBytes token-aware-rewrites every IDENT-context occurrence
// of oldIdent in src to newExpr. Only complete identifier tokens are
// matched; the function never rewrites a substring that happens to appear
// inside a longer identifier, string, or comment.
//
// Used by emitArmBody to swap `&`-bound pattern names with their in-place
// field-access expressions. Falls back to the original bytes if the source
// can't be tokenised (e.g. it carried a non-Go syntax fragment).
func substituteIdentBytes(src []byte, oldIdent, newExpr string) []byte {
	if !strings.Contains(string(src), oldIdent) {
		return src
	}
	var s scanner.Scanner
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(src))
	s.Init(file, src, nil, scanner.ScanComments)

	type span struct{ start, end int }
	var hits []span
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		if tok != token.IDENT || lit != oldIdent {
			continue
		}
		start := fset.Position(pos).Offset
		end := start + len(lit)
		hits = append(hits, span{start, end})
	}
	if len(hits) == 0 {
		return src
	}

	result := make([]byte, 0, len(src)+len(hits)*len(newExpr))
	cursor := 0
	for _, h := range hits {
		result = append(result, src[cursor:h.start]...)
		result = append(result, newExpr...)
		cursor = h.end
	}
	result = append(result, src[cursor:]...)
	return result
}
