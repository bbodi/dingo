package codegen

import (
	"fmt"

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

	g.Write(fmt.Sprintf("switch %s.tag {\n", scrutVar))

	for _, arm := range g.Match.Arms {
		g.emitSharedFieldsArm(enumName, scrutVar, arm)
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

// emitSharedFieldsArm writes one `case` clause (or `default`) for a single
// match arm.
func (g *MatchCodeGen) emitSharedFieldsArm(enumName, scrutVar string, arm *ast.MatchArm) {
	switch pat := arm.Pattern.(type) {
	case *ast.ConstructorPattern:
		g.Write(fmt.Sprintf("case %sTag%s:\n", enumName, pat.Name))
		g.emitSharedFieldsBindings(enumName, scrutVar, pat)
		g.emitArmBody(arm)

	case *ast.WildcardPattern:
		g.Write("default:\n")
		g.emitArmBody(arm)

	case *ast.VariablePattern:
		// `x => ...` rebinds the whole scrutinee. With shared-fields
		// enums the scrutinee is *<Enum>, so this is just an alias.
		g.Write("default:\n")
		g.Write(fmt.Sprintf("%s := %s\n", pat.Name, scrutVar))
		_ = pat
		g.emitArmBody(arm)

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
func (g *MatchCodeGen) emitSharedFieldsBindings(enumName, scrutVar string, pat *ast.ConstructorPattern) {
	if len(pat.Params) == 0 {
		return
	}

	// patternBindingName extracts the binding name for a single param in a
	// constructor pattern. Both VariablePattern and *nullary*
	// ConstructorPattern act as named bindings — the latter happens when
	// the Pratt parser treats an uppercase identifier inside a constructor
	// pattern's params as a nullary constructor (its uppercase-heuristic
	// in isNullaryConstructor). For matching named-struct variants, that
	// identifier IS just the (uppercase) binding name. Wildcards and
	// literals don't bind.
	bindingName := func(p ast.Pattern) (string, bool) {
		switch v := p.(type) {
		case *ast.VariablePattern:
			return v.Name, true
		case *ast.ConstructorPattern:
			if len(v.Params) == 0 {
				return v.Name, true
			}
		}
		return "", false
	}

	// Determine which params actually produce bindings.
	hasBinding := false
	for _, p := range pat.Params {
		if _, ok := bindingName(p); ok {
			hasBinding = true
			break
		}
	}
	if !hasBinding {
		return
	}

	// Emit the cast.
	dataStruct := fmt.Sprintf("*%s%sData", enumName, pat.Name)
	dataVar := g.SharedTempVar("d")
	g.Write(fmt.Sprintf("%s := %s.data.(%s)\n", dataVar, scrutVar, dataStruct))

	// Bindings: for each param that names a binding, emit
	// `<name> := <dataVar>.<FieldName>`. For named struct variants the
	// field name is the binding's own name; for tuple variants we fall
	// back to Value/Value0/.... Pattern is identical to the classic
	// codegen's extractBindings, but we re-derive it here because we
	// don't have the parent type-switch's `v` variable.
	for _, param := range pat.Params {
		name, ok := bindingName(param)
		if !ok {
			continue
		}
		// Tuple variants use Value/ValueN naming; we don't yet have
		// metadata here to detect that. Default to the binding name
		// (struct-variant convention), which is what Option B enums
		// will produce in practice.
		g.Write(fmt.Sprintf("%s := %s.%s\n", name, dataVar, name))
	}
}

// emitArmBody writes the body of an arm. Mirrors the classic codegen's
// per-arm body emission: expression matches return the body; statement
// matches inline it. Guards are wrapped in an if-block. Re-implemented
// here (rather than calling generateArmBody) to avoid relying on the
// `scrutineeTempVar` / `Binding` plumbing used by the type-switch path.
func (g *MatchCodeGen) emitArmBody(arm *ast.MatchArm) {
	if arm.Guard != nil {
		guard := GenerateExpr(arm.Guard)
		g.Write(fmt.Sprintf("if %s {\n", string(guard.Output)))
	}

	body := GenerateExpr(arm.Body)
	_, isReturnExpr := arm.Body.(*ast.ReturnExpr)
	if g.Match.IsExpr && !isReturnExpr {
		g.Write(fmt.Sprintf("return %s\n", string(body.Output)))
	} else {
		g.Buf.Write(body.Output)
		g.WriteByte('\n')
	}

	if arm.Guard != nil {
		g.Write("}\n")
	}
}
