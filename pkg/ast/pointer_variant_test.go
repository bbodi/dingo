package ast

import (
	"strings"
	"testing"
)

// TestEnumParser_PointerVariant exercises the `*Name` prefix on enum
// variants, which marks the variant as pointer-stored.
func TestEnumParser_PointerVariant(t *testing.T) {
	tests := []struct {
		name        string
		src         string
		variantName string
		wantPointer bool
	}{
		{
			name:        "value variant (no prefix)",
			src:         `enum E { Foo }`,
			variantName: "Foo",
			wantPointer: false,
		},
		{
			name:        "pointer unit variant",
			src:         `enum E { *Foo }`,
			variantName: "Foo",
			wantPointer: true,
		},
		{
			name:        "pointer struct variant",
			src:         `enum E { *Foo { x: int } }`,
			variantName: "Foo",
			wantPointer: true,
		},
		{
			name:        "pointer tuple variant",
			src:         `enum E { *Foo(int, string) }`,
			variantName: "Foo",
			wantPointer: true,
		},
		{
			name:        "pointer prefix with surrounding whitespace",
			src:         `enum E { *  Foo { x: int } }`,
			variantName: "Foo",
			wantPointer: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewEnumParser([]byte(tt.src), 0)
			decl, _, err := p.ParseEnumDecl()
			if err != nil {
				t.Fatalf("ParseEnumDecl(%q) returned error: %v", tt.src, err)
			}
			if len(decl.Variants) != 1 {
				t.Fatalf("expected 1 variant, got %d", len(decl.Variants))
			}
			v := decl.Variants[0]
			if v.Name.Name != tt.variantName {
				t.Errorf("variant name = %q, want %q", v.Name.Name, tt.variantName)
			}
			if v.Pointer != tt.wantPointer {
				t.Errorf("Pointer = %v, want %v", v.Pointer, tt.wantPointer)
			}
		})
	}
}

// TestEnumParser_MixedPointerAndValueVariants ensures that pointer and
// value variants can coexist in the same enum.
func TestEnumParser_MixedPointerAndValueVariants(t *testing.T) {
	src := `enum Tree { *Node { value: int }, Leaf }`
	p := NewEnumParser([]byte(src), 0)
	decl, _, err := p.ParseEnumDecl()
	if err != nil {
		t.Fatalf("ParseEnumDecl returned error: %v", err)
	}
	if len(decl.Variants) != 2 {
		t.Fatalf("expected 2 variants, got %d", len(decl.Variants))
	}
	if !decl.Variants[0].Pointer {
		t.Errorf("variant Node should be pointer, got value")
	}
	if decl.Variants[1].Pointer {
		t.Errorf("variant Leaf should be value, got pointer")
	}
}

// TestEnumVariant_String verifies the `*` prefix round-trips in String().
func TestEnumVariant_String_Pointer(t *testing.T) {
	v := &EnumVariant{
		Name:    &Ident{Name: "Node"},
		Kind:    UnitVariant,
		Pointer: true,
	}
	if got, want := v.String(), "*Node"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

// TestEnumCodeGen_PointerVariant covers code generation for pointer-stored
// variants: pointer receiver on the marker method and `&T{...}` in the
// constructor. Value variants keep their original codegen.
func TestEnumCodeGen_PointerVariant(t *testing.T) {
	src := `enum Tree { *Node { value: int }, Leaf }`
	p := NewEnumParser([]byte(src), 0)
	decl, _, err := p.ParseEnumDecl()
	if err != nil {
		t.Fatalf("ParseEnumDecl returned error: %v", err)
	}

	cg := NewEnumCodeGen()
	got := string(cg.Generate(decl, "", 0, 0))

	wants := []string{
		"func (*TreeNode) isTree() {}",                                   // pointer receiver for Node
		"NewTreeNode(value int) Tree { return &TreeNode{value: value} }", // & in constructor
		"func (TreeLeaf) isTree() {}",                                    // value receiver for Leaf
		"NewTreeLeaf() Tree { return TreeLeaf{} }",                       // no & for Leaf
	}
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("generated code missing %q.\nFull output:\n%s", want, got)
		}
	}
}

// TestEnumRegistry_PointerVariants covers IsPointerVariant and the
// registry's PointerVariants set.
func TestEnumRegistry_PointerVariants(t *testing.T) {
	r := NewEnumRegistry()
	r.RegisterSumTypeVariant("Node", "Tree", true)
	r.RegisterSumTypeVariant("Leaf", "Tree", false)

	if !r.IsPointerVariant("Node") {
		t.Errorf("expected Node to be pointer-stored")
	}
	if r.IsPointerVariant("Leaf") {
		t.Errorf("expected Leaf to be value-stored")
	}
	if r.IsPointerVariant("Unknown") {
		t.Errorf("unknown variant should return false")
	}

	// nil receiver guard
	var nilReg *EnumRegistry
	if nilReg.IsPointerVariant("Node") {
		t.Errorf("nil registry should return false")
	}
}

// TestExtractFullEnumRegistry_PropagatesPointer verifies the registry
// extraction populates PointerVariants from the parsed enum declaration.
func TestExtractFullEnumRegistry_PropagatesPointer(t *testing.T) {
	src := []byte(`
package main

enum Tree {
    *Node { value: int }
    Leaf
}
`)
	r := ExtractFullEnumRegistry(src)
	if r == nil {
		t.Fatal("expected non-nil registry")
	}
	if !r.IsPointerVariant("Node") {
		t.Errorf("expected Node to be registered as pointer variant")
	}
	if r.IsPointerVariant("Leaf") {
		t.Errorf("expected Leaf to be value variant")
	}
}

// TestTransformSource_PointerVariantEndToEnd is the end-to-end regression:
// a Dingo enum with a `*Variant` declaration must transpile to Go that
// uses pointer receivers, pointer constructors, and value receivers for
// the non-pointer variants.
func TestTransformSource_PointerVariantEndToEnd(t *testing.T) {
	src := []byte(`package main

enum Tree {
    *Node { value: int, left: Tree, right: Tree }
    Leaf
}
`)
	got, err := TransformSource(src, "test.dingo")
	if err != nil {
		t.Fatalf("TransformSource returned error: %v", err)
	}
	out := string(got)

	wants := []string{
		"func (*TreeNode) isTree() {}",
		"return &TreeNode{",
		"func (TreeLeaf) isTree() {}",
		"return TreeLeaf{}",
	}
	for _, want := range wants {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in transformed Go, got:\n%s", want, out)
		}
	}
}
