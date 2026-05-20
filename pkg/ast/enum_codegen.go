package ast

import (
	"bytes"
	"fmt"
	"go/token"
)

// EnumCodeGen generates Go code from EnumDecl AST nodes.
// This replaces the string-based transformEnum function with proper AST-based generation.
type EnumCodeGen struct {
	buf bytes.Buffer
}

// NewEnumCodeGen creates a new enum code generator.
func NewEnumCodeGen() *EnumCodeGen {
	return &EnumCodeGen{}
}

// Generate produces Go code for an EnumDecl.
// If filename and position are provided, emits //line directive at start.
// Returns the generated Go code as bytes.
func (g *EnumCodeGen) Generate(decl *EnumDecl, filename string, line, col int) []byte {
	g.buf.Reset()

	// Emit //line directive at start (all enum code maps to declaration line)
	if filename != "" && line > 0 && col > 0 {
		directive := FormatLineDirective(filename, line, col)
		g.buf.WriteString(directive)
	}

	// Dispatch to layout-specific codegen. The shared-fields layout
	// (Option B) lowers the enum to a *struct* so methods can be
	// defined directly on it; the classic layout uses an interface.
	if decl.HasSharedFields() {
		g.generateSharedFieldsLayout(decl)
		return g.buf.Bytes()
	}

	enumName := decl.Name.Name
	interfaceMethod := "is" + enumName
	typeParams := g.getTypeParams(decl)

	// 1. Generate interface type with unexported marker method
	g.buf.WriteString("type ")
	g.buf.WriteString(enumName)
	g.buf.WriteString(typeParams)
	g.buf.WriteString(" interface { ")
	g.buf.WriteString(interfaceMethod)
	g.buf.WriteString("() }\n\n")

	// 2. Generate variant structs, marker methods, and constructors
	for _, variant := range decl.Variants {
		g.generateVariant(enumName, interfaceMethod, typeParams, variant)
	}

	return g.buf.Bytes()
}

// generateSharedFieldsLayout emits the Option B layout:
//   - <Enum>Tag uint8 with one constant per variant
//   - <Enum>VariantData sealed marker interface (non-generic — the marker
//     method is parameterless, so the interface doesn't need type params)
//   - <Enum>[T any] struct holding shared fields + tag + data
//   - <Enum><Variant>Data[T any] struct per variant (variant-specific only)
//   - New<Enum><Variant>[T any](...) constructors taking shared + variant args
//
// Generics: the tag, tag-String and VariantData interface stay non-generic
// (they don't carry type information). Everything that can hold or
// produce a typed value — the enum struct, variant Data structs, and
// constructors — gets the type-parameter list. Note: match codegen does
// not yet inject type arguments when casting `e.data` for a generic
// shared-fields enum (the case is opt-in via the `shared` block and not
// currently exercised by the driving use case); see TODO in
// match_shared_fields.go.
func (g *EnumCodeGen) generateSharedFieldsLayout(decl *EnumDecl) {
	enumName := decl.Name.Name
	tagName := enumName + "Tag"
	dataIface := enumName + "VariantData"
	dataMarker := "is" + dataIface

	// typeParams = "[T any]" or "" — full constraint form for declaration sites.
	// typeArgs   = "[T]"      or "" — bare form for instantiation sites.
	typeParams := g.getTypeParams(decl)
	typeArgs := g.getTypeArgs(decl)

	// 1. Tag type + constants (NEVER generic — same set of tags
	//    regardless of type parameters).
	g.buf.WriteString("type ")
	g.buf.WriteString(tagName)
	g.buf.WriteString(" uint8\n\n")

	g.buf.WriteString("const (\n")
	for i, v := range decl.Variants {
		g.buf.WriteString("\t")
		g.buf.WriteString(tagName)
		g.buf.WriteString(v.Name.Name)
		if i == 0 {
			g.buf.WriteString(" ")
			g.buf.WriteString(tagName)
			g.buf.WriteString(" = iota")
		}
		g.buf.WriteString("\n")
	}
	g.buf.WriteString(")\n\n")

	// 2. Tag String() for debugging / panic messages (non-generic).
	g.buf.WriteString("func (t ")
	g.buf.WriteString(tagName)
	g.buf.WriteString(") String() string {\n\tswitch t {\n")
	for _, v := range decl.Variants {
		g.buf.WriteString("\tcase ")
		g.buf.WriteString(tagName)
		g.buf.WriteString(v.Name.Name)
		g.buf.WriteString(": return \"")
		g.buf.WriteString(v.Name.Name)
		g.buf.WriteString("\"\n")
	}
	g.buf.WriteString("\t}\n\treturn \"")
	g.buf.WriteString(tagName)
	g.buf.WriteString("(?)\"\n}\n\n")

	// 3. Sealed marker interface for variant payloads (non-generic —
	//    the method has no parameters or return type, so it works
	//    across all instantiations).
	g.buf.WriteString("type ")
	g.buf.WriteString(dataIface)
	g.buf.WriteString(" interface { ")
	g.buf.WriteString(dataMarker)
	g.buf.WriteString("() }\n\n")

	// 4. Enum struct: shared fields + tag + data.
	//    Generic if the enum has type parameters.
	g.buf.WriteString("type ")
	g.buf.WriteString(enumName)
	g.buf.WriteString(typeParams)
	g.buf.WriteString(" struct {\n")
	for _, f := range decl.SharedFields {
		g.buf.WriteString("\t")
		g.buf.WriteString(f.Name.Name)
		g.buf.WriteString(" ")
		g.buf.WriteString(f.Type.Text)
		g.buf.WriteString("\n")
	}
	g.buf.WriteString("\ttag ")
	g.buf.WriteString(tagName)
	g.buf.WriteString("\n")
	g.buf.WriteString("\tdata ")
	g.buf.WriteString(dataIface)
	g.buf.WriteString("\n}\n\n")

	// 5. Tag accessor on the enum struct.
	// `Tag()` is the public counterpart of the private `tag` field.
	// Receiver carries the type-parameter list when generic.
	g.buf.WriteString("func (e *")
	g.buf.WriteString(enumName)
	g.buf.WriteString(typeArgs)
	g.buf.WriteString(") Tag() ")
	g.buf.WriteString(tagName)
	g.buf.WriteString(" { return e.tag }\n\n")

	// 6. Per-variant Data structs + marker + constructor.
	for _, v := range decl.Variants {
		g.generateSharedFieldsVariant(decl, enumName, tagName, dataMarker, typeParams, typeArgs, v)
	}
}

// getTypeArgs returns the bare type-argument list `[T, E]` used at
// instantiation sites (`&Foo[T, E]{...}`), or "" if non-generic.
// Compared with getTypeParams (`[T, E any]`), this strips the constraint.
func (g *EnumCodeGen) getTypeArgs(decl *EnumDecl) string {
	if decl.TypeParams == nil || len(decl.TypeParams.Params) == 0 {
		return ""
	}
	var b bytes.Buffer
	b.WriteString("[")
	for i, p := range decl.TypeParams.Params {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(p.Name)
	}
	b.WriteString("]")
	return b.String()
}

func (g *EnumCodeGen) generateSharedFieldsVariant(decl *EnumDecl, enumName, tagName, dataMarker, typeParams, typeArgs string, v *EnumVariant) {
	dataStruct := enumName + v.Name.Name + "Data"

	// Variant Data struct: only variant-specific fields (no shared).
	// Generic if the enum is generic.
	g.buf.WriteString("type ")
	g.buf.WriteString(dataStruct)
	g.buf.WriteString(typeParams)
	g.buf.WriteString(" struct {")
	if len(v.Fields) > 0 {
		for i, f := range v.Fields {
			if i > 0 {
				g.buf.WriteString(";")
			}
			g.buf.WriteString(" ")
			g.buf.WriteString(g.getFieldName(v, f, i))
			g.buf.WriteString(" ")
			g.buf.WriteString(f.Type.Text)
		}
		g.buf.WriteString(" ")
	}
	g.buf.WriteString("}\n")

	// Marker method so the Data struct satisfies the sealed VariantData iface.
	// Receiver uses bare type args (`*FooData[T]`).
	g.buf.WriteString("func (*")
	g.buf.WriteString(dataStruct)
	g.buf.WriteString(typeArgs)
	g.buf.WriteString(") ")
	g.buf.WriteString(dataMarker)
	g.buf.WriteString("() {}\n")

	// Constructor: takes shared fields first, then variant-specific fields.
	// Returns *Enum[T] (always pointer-stored — one canonical concrete
	// type usable as a method receiver). The type-params block goes on
	// the function name; the return and inner allocations use bare args.
	ctorName := "New" + enumName + v.Name.Name
	g.buf.WriteString("func ")
	g.buf.WriteString(ctorName)
	g.buf.WriteString(typeParams)
	g.buf.WriteString("(")
	first := true
	for _, f := range decl.SharedFields {
		if !first {
			g.buf.WriteString(", ")
		}
		first = false
		g.buf.WriteString(f.Name.Name)
		g.buf.WriteString(" ")
		g.buf.WriteString(f.Type.Text)
	}
	for i, f := range v.Fields {
		if !first {
			g.buf.WriteString(", ")
		}
		first = false
		g.buf.WriteString(g.getParameterName(v, f, i))
		g.buf.WriteString(" ")
		g.buf.WriteString(f.Type.Text)
	}
	g.buf.WriteString(") *")
	g.buf.WriteString(enumName)
	g.buf.WriteString(typeArgs)
	g.buf.WriteString(" {\n\treturn &")
	g.buf.WriteString(enumName)
	g.buf.WriteString(typeArgs)
	g.buf.WriteString("{")
	// shared field initializers
	first = true
	for _, f := range decl.SharedFields {
		if !first {
			g.buf.WriteString(", ")
		}
		first = false
		g.buf.WriteString(f.Name.Name)
		g.buf.WriteString(": ")
		g.buf.WriteString(f.Name.Name)
	}
	if !first {
		g.buf.WriteString(", ")
	}
	g.buf.WriteString("tag: ")
	g.buf.WriteString(tagName)
	g.buf.WriteString(v.Name.Name)
	g.buf.WriteString(", data: &")
	g.buf.WriteString(dataStruct)
	g.buf.WriteString(typeArgs)
	g.buf.WriteString("{")
	for i, f := range v.Fields {
		if i > 0 {
			g.buf.WriteString(", ")
		}
		fieldName := g.getFieldName(v, f, i)
		paramName := g.getParameterName(v, f, i)
		g.buf.WriteString(fieldName)
		g.buf.WriteString(": ")
		g.buf.WriteString(paramName)
	}
	g.buf.WriteString("}}\n}\n\n")
}

// getTypeParams returns the type parameters string (e.g., "[T, E any]") or empty string
func (g *EnumCodeGen) getTypeParams(decl *EnumDecl) string {
	if decl.TypeParams == nil || len(decl.TypeParams.Params) == 0 {
		return ""
	}

	var buf bytes.Buffer
	buf.WriteString("[")
	for i, param := range decl.TypeParams.Params {
		if i > 0 {
			buf.WriteString(", ")
		}
		buf.WriteString(param.Name)
	}
	buf.WriteString(" any]")
	return buf.String()
}

// generateVariant generates struct, interface method, and constructor for one variant
func (g *EnumCodeGen) generateVariant(enumName, interfaceMethod, typeParams string, variant *EnumVariant) {
	structName := enumName + variant.Name.Name

	// Struct definition
	g.buf.WriteString("type ")
	g.buf.WriteString(structName)
	g.buf.WriteString(typeParams)
	g.buf.WriteString(" struct {")

	if len(variant.Fields) > 0 {
		for i, field := range variant.Fields {
			if i > 0 {
				g.buf.WriteString("; ")
			}

			fieldName := g.getFieldName(variant, field, i)
			g.buf.WriteString(fieldName)
			g.buf.WriteString(" ")
			g.buf.WriteString(field.Type.Text)
		}
	}
	g.buf.WriteString("}\n")

	// Interface method.
	// Pointer-stored variants get a pointer receiver so only *T satisfies
	// the enum interface (not value T). This matches the `case *T:` form
	// emitted by match codegen for the same variant.
	g.buf.WriteString("func (")
	if variant.Pointer {
		g.buf.WriteString("*")
	}
	g.buf.WriteString(structName)
	g.buf.WriteString(typeParams)
	g.buf.WriteString(") ")
	g.buf.WriteString(interfaceMethod)
	g.buf.WriteString("() {}\n")

	// Constructor function
	g.generateConstructor(enumName, typeParams, variant)
	g.buf.WriteString("\n")
}

// generateConstructor generates a constructor function for a variant
func (g *EnumCodeGen) generateConstructor(enumName, typeParams string, variant *EnumVariant) {
	structName := enumName + variant.Name.Name
	constructorName := "New" + enumName + variant.Name.Name

	g.buf.WriteString("func ")
	g.buf.WriteString(constructorName)
	g.buf.WriteString(typeParams)
	g.buf.WriteString("(")

	// Parameters
	for i, field := range variant.Fields {
		if i > 0 {
			g.buf.WriteString(", ")
		}
		paramName := g.getParameterName(variant, field, i)
		g.buf.WriteString(paramName)
		g.buf.WriteString(" ")
		g.buf.WriteString(field.Type.Text)
	}

	g.buf.WriteString(") ")
	g.buf.WriteString(enumName)
	g.buf.WriteString(typeParams)
	g.buf.WriteString(" { return ")
	// Pointer-stored variants return &T{...}.
	if variant.Pointer {
		g.buf.WriteString("&")
	}
	g.buf.WriteString(structName)
	g.buf.WriteString(typeParams)
	g.buf.WriteString("{")

	// Field initializers
	for i, field := range variant.Fields {
		if i > 0 {
			g.buf.WriteString(", ")
		}
		fieldName := g.getFieldName(variant, field, i)
		paramName := g.getParameterName(variant, field, i)
		g.buf.WriteString(fieldName)
		g.buf.WriteString(": ")
		g.buf.WriteString(paramName)
	}

	g.buf.WriteString("} }\n")
}

// getFieldName returns the appropriate field name for a variant field (struct field, uppercase)
func (g *EnumCodeGen) getFieldName(variant *EnumVariant, field *EnumField, index int) string {
	// Struct variant with named fields
	if field.Name != nil {
		return field.Name.Name
	}

	// Tuple variant - use "Value" for single field, "Value0", "Value1" for multiple
	if len(variant.Fields) == 1 {
		return "Value"
	}
	return fmt.Sprintf("Value%d", index)
}

// getParameterName returns the appropriate parameter name for a constructor parameter (lowercase)
func (g *EnumCodeGen) getParameterName(variant *EnumVariant, field *EnumField, index int) string {
	// Struct variant with named fields - use field name as-is
	if field.Name != nil {
		return field.Name.Name
	}

	// Tuple variant - use "value" for single field, "value0", "value1" for multiple
	if len(variant.Fields) == 1 {
		return "value"
	}
	return fmt.Sprintf("value%d", index)
}

// ExtractEnumRegistry extracts the enum registry from Dingo source without transforming it.
// This is useful when you need the registry for match expressions but don't want to
// re-transform the enum declarations.
func ExtractEnumRegistry(src []byte) map[string]string {
	enumPositions := FindEnumDeclarations(src)
	if len(enumPositions) == 0 {
		return nil
	}

	registry := make(map[string]string)

	for _, enumStart := range enumPositions {
		parser := NewEnumParser(src[enumStart:], enumStart)
		decl, _, err := parser.ParseEnumDecl()
		if err != nil {
			continue
		}

		// Register variants for match expression lookup
		for _, v := range decl.Variants {
			registry[v.Name.Name] = decl.Name.Name
		}
	}

	return registry
}

// ExtractFullEnumRegistry extracts the full enum registry from Dingo source without transforming it.
// This includes both sum type enums and value enums with their metadata.
// Returns nil if no enums are found.
func ExtractFullEnumRegistry(src []byte) *EnumRegistry {
	enumPositions := FindEnumDeclarations(src)
	if len(enumPositions) == 0 {
		return nil
	}

	registry := NewEnumRegistry()

	for _, enumStart := range enumPositions {
		// Check if this is a value enum
		if IsValueEnum(src[enumStart:]) {
			// Look for attribute before the enum
			declStart := findAttributeStart(src, enumStart)

			// Parse as value enum with potential attributes
			parser := NewValueEnumParser(src[declStart:], declStart)
			decl, _, err := parser.ParseValueEnumWithAttributes()
			if err != nil {
				continue
			}

			// Check @prefix attribute
			usePrefix, _ := ValidatePrefixAttribute(decl.Attributes)

			// Register value enum variants
			variantNames := make([]string, len(decl.Variants))
			for i, v := range decl.Variants {
				variantNames[i] = v.Name.Name
			}
			registry.RegisterValueEnum(decl.Name.Name, variantNames, usePrefix)
		} else {
			// Sum type enum
			parser := NewEnumParser(src[enumStart:], enumStart)
			decl, _, err := parser.ParseEnumDecl()
			if err != nil {
				continue
			}

			// Register sum type variants
			for _, v := range decl.Variants {
				registry.RegisterSumTypeVariant(v.Name.Name, decl.Name.Name, v.Pointer)
			}
			// If the enum uses the shared-fields layout (Option B), record
			// that so match codegen dispatches on `tag` rather than via
			// a Go type switch.
			if decl.HasSharedFields() {
				registry.RegisterSharedFieldsEnum(decl.Name.Name)
			}
		}
	}

	return registry
}

// TransformEnumSource transforms Dingo source containing enums to Go source.
// This is the main entry point that handles both sum type enums and value enums.
// If filename is provided, emits //line directives for accurate error reporting.
//
// Returns:
//   - transformed source code
//   - legacy registry (map[string]string) for backward compatibility
//
// For the new EnumRegistry with value enum support, use TransformEnumSourceWithRegistry.
func TransformEnumSource(src []byte, filename string) ([]byte, map[string]string) {
	result, registry := TransformEnumSourceWithRegistry(src, filename)
	if registry == nil {
		return result, nil
	}
	return result, registry.ToLegacyMap()
}

// TransformEnumSourceWithRegistry transforms Dingo source containing enums to Go source.
// Returns the new EnumRegistry which supports both sum types and value enums.
func TransformEnumSourceWithRegistry(src []byte, filename string) ([]byte, *EnumRegistry) {
	// Use the unified transform function from value_enum_codegen.go
	// which handles both value enums and sum types
	return TransformValueEnumSource(src, filename)
}

// offsetToLineCol converts a byte offset in source to 1-indexed line:col.
// Returns (0, 0) if offset is invalid.
//
// This uses Go's token.FileSet which handles line counting internally.
// The FileSet is the proper token-based approach for position tracking.
func offsetToLineCol(src []byte, offset int) (line, col int) {
	if offset < 0 || offset >= len(src) {
		return 0, 0
	}

	// Create a FileSet and add the source file
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(src))

	// SetLinesForContent scans the source and records newline positions
	// This is the token-based way to set up line info
	file.SetLinesForContent(src)

	// Convert byte offset to token.Pos, then to Position (line:col)
	pos := file.Pos(offset)
	position := fset.Position(pos)

	return position.Line, position.Column
}
