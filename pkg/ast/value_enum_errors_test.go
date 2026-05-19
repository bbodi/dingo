package ast

import (
	"go/token"
	"strings"
	"testing"
)

// =============================================================================
// Phase 4: Error Handling and Validation Tests
// =============================================================================

func TestValidateValueEnumDecl_EmptyEnum(t *testing.T) {
	// enum Status: int {} - ERROR: value enum must have at least one variant
	decl := &ValueEnumDecl{
		Enum:     token.Pos(1),
		Name:     &Ident{NamePos: token.Pos(6), Name: "Status"},
		Colon:    token.Pos(12),
		BaseType: &TypeExpr{StartPos: token.Pos(14), EndPos: token.Pos(17), Text: "int"},
		LBrace:   token.Pos(18),
		Variants: []*ValueEnumVariant{}, // Empty!
		RBrace:   token.Pos(19),
	}

	result := ValidateValueEnumDecl(decl, nil)

	if !result.HasErrors() {
		t.Fatal("expected error for empty enum, got none")
	}

	if len(result.Errors) != 1 {
		t.Errorf("expected 1 error, got %d", len(result.Errors))
	}

	err := result.Errors[0]
	if err.Kind != ErrorEmptyEnum {
		t.Errorf("expected ErrorEmptyEnum, got %v", err.Kind)
	}
	if !strings.Contains(err.Message, "must have at least one variant") {
		t.Errorf("error message should mention 'must have at least one variant', got: %s", err.Message)
	}
	if !strings.Contains(err.Message, "Status") {
		t.Errorf("error message should mention enum name 'Status', got: %s", err.Message)
	}
}

func TestValidateValueEnumDecl_InvalidBaseType(t *testing.T) {
	// enum Status: float64 { Active } - ERROR: invalid base type
	decl := &ValueEnumDecl{
		Enum:     token.Pos(1),
		Name:     &Ident{NamePos: token.Pos(6), Name: "Status"},
		Colon:    token.Pos(12),
		BaseType: &TypeExpr{StartPos: token.Pos(14), EndPos: token.Pos(21), Text: "float64"},
		LBrace:   token.Pos(22),
		Variants: []*ValueEnumVariant{
			{Name: &Ident{NamePos: token.Pos(24), Name: "Active"}},
		},
		RBrace: token.Pos(30),
	}

	result := ValidateValueEnumDecl(decl, nil)

	if !result.HasErrors() {
		t.Fatal("expected error for invalid base type, got none")
	}

	found := false
	for _, err := range result.Errors {
		if err.Kind == ErrorInvalidBaseType {
			found = true
			if !strings.Contains(err.Message, "float64") {
				t.Errorf("error message should mention 'float64', got: %s", err.Message)
			}
			if !strings.Contains(err.Message, "string, int") {
				t.Errorf("error message should suggest valid types, got: %s", err.Message)
			}
		}
	}
	if !found {
		t.Error("expected ErrorInvalidBaseType in errors")
	}
}

func TestValidateValueEnumDecl_StringRequiresValue(t *testing.T) {
	// enum Key: string { UserID } - ERROR: string enum variant requires explicit value
	decl := &ValueEnumDecl{
		Enum:     token.Pos(1),
		Name:     &Ident{NamePos: token.Pos(6), Name: "Key"},
		Colon:    token.Pos(9),
		BaseType: &TypeExpr{StartPos: token.Pos(11), EndPos: token.Pos(17), Text: "string"},
		LBrace:   token.Pos(18),
		Variants: []*ValueEnumVariant{
			{Name: &Ident{NamePos: token.Pos(20), Name: "UserID"}, Value: nil}, // Missing value!
		},
		RBrace: token.Pos(26),
	}

	result := ValidateValueEnumDecl(decl, nil)

	if !result.HasErrors() {
		t.Fatal("expected error for string enum without value, got none")
	}

	found := false
	for _, err := range result.Errors {
		if err.Kind == ErrorStringRequiresValue {
			found = true
			if !strings.Contains(err.Message, "UserID") {
				t.Errorf("error message should mention variant 'UserID', got: %s", err.Message)
			}
			if !strings.Contains(err.Message, "requires explicit value") {
				t.Errorf("error message should mention 'requires explicit value', got: %s", err.Message)
			}
		}
	}
	if !found {
		t.Error("expected ErrorStringRequiresValue in errors")
	}
}

func TestValidateValueEnumDecl_DuplicateVariant(t *testing.T) {
	// enum Status: int { Pending, Active, Pending } - ERROR: duplicate variant
	decl := &ValueEnumDecl{
		Enum:     token.Pos(1),
		Name:     &Ident{NamePos: token.Pos(6), Name: "Status"},
		Colon:    token.Pos(12),
		BaseType: &TypeExpr{StartPos: token.Pos(14), EndPos: token.Pos(17), Text: "int"},
		LBrace:   token.Pos(18),
		Variants: []*ValueEnumVariant{
			{Name: &Ident{NamePos: token.Pos(20), Name: "Pending"}},
			{Name: &Ident{NamePos: token.Pos(29), Name: "Active"}},
			{Name: &Ident{NamePos: token.Pos(37), Name: "Pending"}}, // Duplicate!
		},
		RBrace: token.Pos(44),
	}

	result := ValidateValueEnumDecl(decl, nil)

	if !result.HasErrors() {
		t.Fatal("expected error for duplicate variant, got none")
	}

	found := false
	for _, err := range result.Errors {
		if err.Kind == ErrorDuplicateVariant {
			found = true
			if !strings.Contains(err.Message, "Pending") {
				t.Errorf("error message should mention 'Pending', got: %s", err.Message)
			}
			if !strings.Contains(err.Message, "duplicate") {
				t.Errorf("error message should mention 'duplicate', got: %s", err.Message)
			}
		}
	}
	if !found {
		t.Error("expected ErrorDuplicateVariant in errors")
	}
}

func TestValidateValueEnumDecl_TypeMismatch(t *testing.T) {
	// enum Status: int { Pending = "not_int" } - ERROR: type mismatch
	decl := &ValueEnumDecl{
		Enum:     token.Pos(1),
		Name:     &Ident{NamePos: token.Pos(6), Name: "Status"},
		Colon:    token.Pos(12),
		BaseType: &TypeExpr{StartPos: token.Pos(14), EndPos: token.Pos(17), Text: "int"},
		LBrace:   token.Pos(18),
		Variants: []*ValueEnumVariant{
			{
				Name:   &Ident{NamePos: token.Pos(20), Name: "Pending"},
				Assign: token.Pos(28),
				Value:  &RawExpr{StartPos: token.Pos(30), EndPos: token.Pos(39), Text: `"not_int"`},
			},
		},
		RBrace: token.Pos(40),
	}

	result := ValidateValueEnumDecl(decl, nil)

	if !result.HasErrors() {
		t.Fatal("expected error for type mismatch, got none")
	}

	found := false
	for _, err := range result.Errors {
		if err.Kind == ErrorTypeMismatch {
			found = true
			if !strings.Contains(err.Message, "Pending") {
				t.Errorf("error message should mention 'Pending', got: %s", err.Message)
			}
			if !strings.Contains(err.Message, "integer") {
				t.Errorf("error message should mention 'integer', got: %s", err.Message)
			}
		}
	}
	if !found {
		t.Error("expected ErrorTypeMismatch in errors")
	}
}

func TestValidateValueEnumDecl_MixedValues_Warning(t *testing.T) {
	// enum Priority: int { Low, Medium = 5, High } - WARNING: mixing explicit and auto
	decl := &ValueEnumDecl{
		Enum:     token.Pos(1),
		Name:     &Ident{NamePos: token.Pos(6), Name: "Priority"},
		Colon:    token.Pos(14),
		BaseType: &TypeExpr{StartPos: token.Pos(16), EndPos: token.Pos(19), Text: "int"},
		LBrace:   token.Pos(20),
		Variants: []*ValueEnumVariant{
			{Name: &Ident{NamePos: token.Pos(22), Name: "Low"}},
			{
				Name:   &Ident{NamePos: token.Pos(27), Name: "Medium"},
				Assign: token.Pos(34),
				Value:  &RawExpr{StartPos: token.Pos(36), EndPos: token.Pos(37), Text: "5"},
			},
			{Name: &Ident{NamePos: token.Pos(39), Name: "High"}},
		},
		RBrace: token.Pos(43),
	}

	result := ValidateValueEnumDecl(decl, nil)

	// Should not have errors, but should have warning
	if result.HasErrors() {
		t.Errorf("unexpected errors: %v", result.Errors)
	}

	if !result.HasWarnings() {
		t.Fatal("expected warning for mixed values, got none")
	}

	found := false
	for _, warn := range result.Warnings {
		if warn.Kind == WarningMixedValues {
			found = true
			if !strings.Contains(warn.Message, "Priority") {
				t.Errorf("warning should mention 'Priority', got: %s", warn.Message)
			}
			if !strings.Contains(warn.Message, "mixes") || !strings.Contains(warn.Message, "explicit") {
				t.Errorf("warning should mention mixing explicit/auto values, got: %s", warn.Message)
			}
		}
	}
	if !found {
		t.Error("expected WarningMixedValues in warnings")
	}
}

func TestValidatePrefixAttributeWithPos_MissingArg(t *testing.T) {
	// @prefix - ERROR: requires boolean argument
	attrs := []*Attribute{
		{
			At:   token.Pos(1),
			Name: &Ident{NamePos: token.Pos(2), Name: "prefix"},
			Args: []Expr{}, // No arguments!
		},
	}

	_, err := ValidatePrefixAttributeWithPos(attrs)

	if err == nil {
		t.Fatal("expected error for @prefix without argument, got none")
	}

	if err.Kind != ErrorMissingPrefixArg {
		t.Errorf("expected ErrorMissingPrefixArg, got %v", err.Kind)
	}
	if !strings.Contains(err.Message, "requires a boolean argument") {
		t.Errorf("error message should mention 'requires a boolean argument', got: %s", err.Message)
	}
}

func TestValidatePrefixAttributeWithPos_TooManyArgs(t *testing.T) {
	// @prefix(true, false) - ERROR: accepts only one argument
	attrs := []*Attribute{
		{
			At:   token.Pos(1),
			Name: &Ident{NamePos: token.Pos(2), Name: "prefix"},
			Args: []Expr{
				&RawExpr{StartPos: token.Pos(9), EndPos: token.Pos(13), Text: "true"},
				&RawExpr{StartPos: token.Pos(15), EndPos: token.Pos(20), Text: "false"},
			},
		},
	}

	_, err := ValidatePrefixAttributeWithPos(attrs)

	if err == nil {
		t.Fatal("expected error for @prefix with too many arguments, got none")
	}

	if err.Kind != ErrorTooManyPrefixArgs {
		t.Errorf("expected ErrorTooManyPrefixArgs, got %v", err.Kind)
	}
	if !strings.Contains(err.Message, "only one argument") {
		t.Errorf("error message should mention 'only one argument', got: %s", err.Message)
	}
}

func TestValidatePrefixAttributeWithPos_InvalidArg(t *testing.T) {
	// @prefix("not a bool") - ERROR: must be boolean literal
	attrs := []*Attribute{
		{
			At:   token.Pos(1),
			Name: &Ident{NamePos: token.Pos(2), Name: "prefix"},
			Args: []Expr{
				&RawExpr{StartPos: token.Pos(9), EndPos: token.Pos(21), Text: `"not a bool"`},
			},
		},
	}

	_, err := ValidatePrefixAttributeWithPos(attrs)

	if err == nil {
		t.Fatal("expected error for @prefix with non-boolean argument, got none")
	}

	if err.Kind != ErrorInvalidPrefixArg {
		t.Errorf("expected ErrorInvalidPrefixArg, got %v", err.Kind)
	}
	if !strings.Contains(err.Message, "true") || !strings.Contains(err.Message, "false") {
		t.Errorf("error message should mention 'true' or 'false', got: %s", err.Message)
	}
}

func TestValidatePrefixAttributeWithPos_ValidTrue(t *testing.T) {
	// @prefix(true) - OK
	attrs := []*Attribute{
		{
			At:   token.Pos(1),
			Name: &Ident{NamePos: token.Pos(2), Name: "prefix"},
			Args: []Expr{
				&RawExpr{StartPos: token.Pos(9), EndPos: token.Pos(13), Text: "true"},
			},
		},
	}

	usePrefix, err := ValidatePrefixAttributeWithPos(attrs)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !usePrefix {
		t.Error("expected usePrefix=true for @prefix(true)")
	}
}

func TestValidatePrefixAttributeWithPos_ValidFalse(t *testing.T) {
	// @prefix(false) - OK
	attrs := []*Attribute{
		{
			At:   token.Pos(1),
			Name: &Ident{NamePos: token.Pos(2), Name: "prefix"},
			Args: []Expr{
				&RawExpr{StartPos: token.Pos(9), EndPos: token.Pos(14), Text: "false"},
			},
		},
	}

	usePrefix, err := ValidatePrefixAttributeWithPos(attrs)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if usePrefix {
		t.Error("expected usePrefix=false for @prefix(false)")
	}
}

func TestEnumRegistry_CollisionDetection(t *testing.T) {
	registry := NewEnumRegistry()

	// Register a sum type with "Ok" variant
	registry.RegisterSumTypeVariant("Ok", "Result", false)

	// Register a value enum with same variant name - should detect collision
	registry.RegisterValueEnum("Status", []string{"Ok", "Error"}, true)

	if !registry.HasCollisions() {
		t.Fatal("expected collision between sum type and value enum 'Ok' variant")
	}

	collisions := registry.CollisionErrors()
	if len(collisions) != 1 {
		t.Errorf("expected 1 collision, got %d", len(collisions))
	}

	if !strings.Contains(collisions[0], "Ok") {
		t.Errorf("collision message should mention 'Ok', got: %s", collisions[0])
	}
	if !strings.Contains(collisions[0], "Result") {
		t.Errorf("collision message should mention 'Result', got: %s", collisions[0])
	}
	if !strings.Contains(collisions[0], "Status") {
		t.Errorf("collision message should mention 'Status', got: %s", collisions[0])
	}
}

func TestEnumRegistry_FormatCollisions(t *testing.T) {
	registry := NewEnumRegistry()

	// No collisions
	if registry.FormatCollisions() != "" {
		t.Error("expected empty string for no collisions")
	}

	// Create collision
	registry.RegisterSumTypeVariant("Active", "State", false)
	registry.RegisterValueEnum("Status", []string{"Active", "Inactive"}, true)

	formatted := registry.FormatCollisions()
	if formatted == "" {
		t.Error("expected non-empty formatted collision string")
	}
	if !strings.Contains(formatted, "Active") {
		t.Errorf("formatted collisions should mention 'Active', got: %s", formatted)
	}
}

func TestValidateValueEnumDecl_ValidEnum_NoErrors(t *testing.T) {
	// enum Status: int { Pending, Active, Closed } - OK
	decl := &ValueEnumDecl{
		Enum:     token.Pos(1),
		Name:     &Ident{NamePos: token.Pos(6), Name: "Status"},
		Colon:    token.Pos(12),
		BaseType: &TypeExpr{StartPos: token.Pos(14), EndPos: token.Pos(17), Text: "int"},
		LBrace:   token.Pos(18),
		Variants: []*ValueEnumVariant{
			{Name: &Ident{NamePos: token.Pos(20), Name: "Pending"}},
			{Name: &Ident{NamePos: token.Pos(29), Name: "Active"}},
			{Name: &Ident{NamePos: token.Pos(37), Name: "Closed"}},
		},
		RBrace: token.Pos(43),
	}

	result := ValidateValueEnumDecl(decl, nil)

	if result.HasErrors() {
		t.Errorf("unexpected errors for valid enum: %v", result.Errors)
	}
	// No mixed values warning since all are auto-increment
	if result.HasWarnings() {
		t.Errorf("unexpected warnings for valid enum: %v", result.Warnings)
	}
}

func TestValidateValueEnumDecl_ValidStringEnum_NoErrors(t *testing.T) {
	// enum Key: string { UserID = "user_id", CompanyID = "company_id" } - OK
	decl := &ValueEnumDecl{
		Enum:     token.Pos(1),
		Name:     &Ident{NamePos: token.Pos(6), Name: "Key"},
		Colon:    token.Pos(9),
		BaseType: &TypeExpr{StartPos: token.Pos(11), EndPos: token.Pos(17), Text: "string"},
		LBrace:   token.Pos(18),
		Variants: []*ValueEnumVariant{
			{
				Name:   &Ident{NamePos: token.Pos(20), Name: "UserID"},
				Assign: token.Pos(27),
				Value:  &RawExpr{StartPos: token.Pos(29), EndPos: token.Pos(38), Text: `"user_id"`},
			},
			{
				Name:   &Ident{NamePos: token.Pos(41), Name: "CompanyID"},
				Assign: token.Pos(51),
				Value:  &RawExpr{StartPos: token.Pos(53), EndPos: token.Pos(65), Text: `"company_id"`},
			},
		},
		RBrace: token.Pos(66),
	}

	result := ValidateValueEnumDecl(decl, nil)

	if result.HasErrors() {
		t.Errorf("unexpected errors for valid string enum: %v", result.Errors)
	}
	if result.HasWarnings() {
		t.Errorf("unexpected warnings for valid string enum: %v", result.Warnings)
	}
}

func TestFormatValidationErrors(t *testing.T) {
	result := NewValidationResult()
	result.AddError(token.Pos(10), ErrorEmptyEnum, "enum 'Status' must have at least one variant")
	result.AddWarning(token.Pos(20), WarningMixedValues, "enum 'Priority' mixes values")

	// Without FileSet - positions shown as raw
	output := FormatValidationErrors(result, nil)

	if !strings.Contains(output, "error:") {
		t.Errorf("output should contain 'error:', got: %s", output)
	}
	if !strings.Contains(output, "warning:") {
		t.Errorf("output should contain 'warning:', got: %s", output)
	}
	if !strings.Contains(output, "Status") {
		t.Errorf("output should contain 'Status', got: %s", output)
	}
	if !strings.Contains(output, "Priority") {
		t.Errorf("output should contain 'Priority', got: %s", output)
	}
}

func TestParseValueEnumDecl_ErrorMessages(t *testing.T) {
	// Integration test: parse and check error messages from parser
	tests := []struct {
		name       string
		src        string
		wantErr    bool
		errContain string
	}{
		{
			name:       "invalid base type",
			src:        "enum Status: float64 { Active }",
			wantErr:    true,
			errContain: "invalid value enum base type",
		},
		{
			name:       "string enum without value",
			src:        "enum Key: string { UserID }",
			wantErr:    true,
			errContain: "requires explicit value",
		},
		{
			name:       "duplicate variant",
			src:        "enum Status: int { Active, Active }",
			wantErr:    true,
			errContain: "duplicate variant",
		},
		{
			name:    "valid int enum",
			src:     "enum Status: int { Pending, Active }",
			wantErr: false,
		},
		{
			name:    "valid string enum",
			src:     `enum Key: string { UserID = "user_id" }`,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parser := NewValueEnumParser([]byte(tt.src), 0)
			_, _, err := parser.ParseValueEnumDecl()

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errContain != "" && !strings.Contains(err.Error(), tt.errContain) {
					t.Errorf("error should contain %q, got: %s", tt.errContain, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestIsStringLiteral(t *testing.T) {
	tests := []struct {
		text   string
		expect bool
	}{
		{`"hello"`, true},
		{`"user_id"`, true},
		{`""`, true},
		{"`raw`", true},
		{"``", true},
		{"hello", false},
		{"123", false},
		{`'c'`, false}, // char literal, not string
		{"", false},
		{`"unclosed`, false},
	}

	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			got := isStringLiteral(tt.text)
			if got != tt.expect {
				t.Errorf("isStringLiteral(%q) = %v, want %v", tt.text, got, tt.expect)
			}
		})
	}
}

func TestIsValidIntegerValue(t *testing.T) {
	tests := []struct {
		text   string
		expect bool
	}{
		{"0", true},
		{"123", true},
		{"-5", true},
		{"0xFF", true},
		{"0x1A", true},
		{"0o755", true},
		{"0b1010", true},
		{"iota", true},
		{"math.MaxInt", true},
		{"SomeConstant", true},
		{`"string"`, false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			got := isValidIntegerValue(tt.text)
			if got != tt.expect {
				t.Errorf("isValidIntegerValue(%q) = %v, want %v", tt.text, got, tt.expect)
			}
		})
	}
}

func TestCombinedErrors(t *testing.T) {
	// Test with no errors
	result := NewValidationResult()
	if result.CombinedErrors() != "" {
		t.Error("expected empty string for no errors")
	}

	// Test with single error
	result.AddError(token.Pos(1), ErrorEmptyEnum, "empty enum")
	combined := result.CombinedErrors()
	if !strings.Contains(combined, "empty enum") {
		t.Errorf("combined should contain 'empty enum', got: %s", combined)
	}

	// Test with multiple errors
	result.AddError(token.Pos(2), ErrorDuplicateVariant, "duplicate variant")
	combined = result.CombinedErrors()
	if !strings.Contains(combined, "multiple errors") {
		t.Errorf("combined with multiple errors should mention 'multiple errors', got: %s", combined)
	}
}
