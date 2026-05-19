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
