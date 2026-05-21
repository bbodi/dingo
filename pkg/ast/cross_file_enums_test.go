package ast

import (
	"os"
	"path/filepath"
	"testing"
)

// TestExtractEnumRegistryFromDir_PicksUpSiblingEnum verifies that an enum
// declared in one .dingo file is visible to the registry built for a
// sibling file. This unblocks `match e { Variant => ... }` expressions in
// files that don't carry the enum declaration themselves.
func TestExtractEnumRegistryFromDir_PicksUpSiblingEnum(t *testing.T) {
	dir := t.TempDir()

	enumFile := filepath.Join(dir, "expr_enum.dingo")
	if err := os.WriteFile(enumFile, []byte(`enum E_ {
    shared { pos: int }
    *SelectorExpr { X: int, Sel: int }
    *NilExpr {}
}
`), 0644); err != nil {
		t.Fatal(err)
	}

	matchFile := filepath.Join(dir, "expr_enum_node.dingo")
	if err := os.WriteFile(matchFile, []byte(`package ir

func (e *E_) Op() int { return 0 }
`), 0644); err != nil {
		t.Fatal(err)
	}

	reg := ExtractEnumRegistryFromDir(matchFile)
	if reg == nil {
		t.Fatalf("ExtractEnumRegistryFromDir returned nil; expected sibling enum to be picked up")
	}
	if !reg.IsSharedFieldsEnum("E_") {
		t.Errorf("E_ should be marked as a shared-fields enum (declared in sibling)")
	}
	if _, ok := reg.IsSumTypeVariant("SelectorExpr"); !ok {
		t.Errorf("SelectorExpr variant should be registered from sibling enum")
	}
	if _, ok := reg.IsSumTypeVariant("NilExpr"); !ok {
		t.Errorf("NilExpr variant should be registered from sibling enum")
	}
}

// TestExtractEnumRegistryFromDir_SkipsSelf verifies that the function does
// not double-count the file it's called for. (The caller already has its
// own per-file registry; the sibling pass should only add NEW enums.)
func TestExtractEnumRegistryFromDir_SkipsSelf(t *testing.T) {
	dir := t.TempDir()

	selfFile := filepath.Join(dir, "self.dingo")
	if err := os.WriteFile(selfFile, []byte(`enum LocalEnum {
    *Foo {}
}
`), 0644); err != nil {
		t.Fatal(err)
	}

	reg := ExtractEnumRegistryFromDir(selfFile)
	// No siblings exist beyond self, so the sibling-only registry is nil.
	if reg != nil {
		if _, ok := reg.IsSumTypeVariant("Foo"); ok {
			t.Errorf("self file's variant should not be registered by the sibling pass")
		}
	}
}

// TestEnumRegistry_Merge verifies that two independent registries union
// cleanly when one contains an enum the other doesn't.
func TestEnumRegistry_Merge(t *testing.T) {
	a := NewEnumRegistry()
	a.RegisterSumTypeVariant("AVar", "EnumA", true)
	a.RegisterSharedFieldsEnum("EnumA")

	b := NewEnumRegistry()
	b.RegisterSumTypeVariant("BVar", "EnumB", false)

	a.Merge(b)

	if _, ok := a.IsSumTypeVariant("AVar"); !ok {
		t.Errorf("Merge lost AVar from receiver")
	}
	if _, ok := a.IsSumTypeVariant("BVar"); !ok {
		t.Errorf("Merge did not pick up BVar from argument")
	}
	if !a.IsSharedFieldsEnum("EnumA") {
		t.Errorf("Merge lost shared-fields marker for EnumA")
	}
	if a.IsPointerVariant("BVar") {
		t.Errorf("Merge incorrectly marked BVar as pointer variant")
	}
}

// TestEnumRegistry_MergeNilArguments verifies Merge is safe on nil
// receivers and arguments.
func TestEnumRegistry_MergeNilArguments(t *testing.T) {
	var nilReg *EnumRegistry
	other := NewEnumRegistry()
	other.RegisterSumTypeVariant("X", "E", true)

	// nil receiver: should not panic
	nilReg.Merge(other)

	// nil argument: should not panic
	r := NewEnumRegistry()
	r.Merge(nil)

	if _, ok := r.IsSumTypeVariant("X"); ok {
		t.Errorf("Merge(nil) should not introduce variants")
	}
}
