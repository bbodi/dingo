package ast

import (
	"strings"
	"testing"
)

// shared_fields_e2e_test.go — end-to-end tests that exercise the
// shared-fields enum layout through the high-level transform entry
// points used by the build pipeline:
//
//   - ExtractFullEnumRegistry: scans dingo source, returns a registry
//     consulted by the match codegen. Must mark shared-fields enums.
//   - TransformValueEnumSource: rewrites dingo source to Go source.
//     Must emit the Option B Go layout for shared-fields enums and the
//     classic interface layout for everything else.
//
// These tests complement the AST-only and codegen-only tests by
// guarding the integration boundary between parser, registry, and
// codegen. They catch wiring bugs where, for example, the parser
// produces SharedFields but the transform pipeline forgets to consult
// HasSharedFields().

// TestExtractFullEnumRegistry_MarksSharedFields verifies that an enum
// with a `shared { ... }` block is recorded as a shared-fields enum in
// the registry returned by ExtractFullEnumRegistry. Without this, match
// codegen would fall back to the classic type-switch path and emit
// invalid Go (the enum is a struct, not an interface).
func TestExtractFullEnumRegistry_MarksSharedFields(t *testing.T) {
	src := []byte(`enum Expr {
    shared { pos: Pos }
    *AddrExpr { X: Node }
    *NilExpr {}
}`)

	r := ExtractFullEnumRegistry(src)
	if r == nil {
		t.Fatalf("ExtractFullEnumRegistry returned nil")
	}

	if !r.IsSharedFieldsEnum("Expr") {
		t.Errorf("Expr should be marked as shared-fields enum")
	}

	// Variants should still be registered for exhaustiveness lookups.
	for _, v := range []string{"AddrExpr", "NilExpr"} {
		if _, ok := r.IsSumTypeVariant(v); !ok {
			t.Errorf("variant %q not registered as sum-type variant", v)
		}
	}
}

// TestExtractFullEnumRegistry_DoesNotMarkClassic verifies the negation:
// a classic enum (no `shared` block) must not be flagged as
// shared-fields, otherwise we'd lower its match to a tag-dispatch that
// doesn't compile.
func TestExtractFullEnumRegistry_DoesNotMarkClassic(t *testing.T) {
	src := []byte(`enum Tree {
    *Node { value: int }
    *Leaf {}
}`)

	r := ExtractFullEnumRegistry(src)
	if r == nil {
		t.Fatalf("ExtractFullEnumRegistry returned nil")
	}
	if r.IsSharedFieldsEnum("Tree") {
		t.Errorf("classic enum Tree should NOT be marked as shared-fields")
	}
}

// TestExtractFullEnumRegistry_MixedFile covers the realistic case where
// a single source file declares both flavours of enum. Each should
// independently end up in the right bucket.
func TestExtractFullEnumRegistry_MixedFile(t *testing.T) {
	src := []byte(`enum Expr {
    shared { pos: Pos }
    *AddrExpr { X: Node }
}

enum Tree {
    *Node { value: int }
    *Leaf {}
}`)

	r := ExtractFullEnumRegistry(src)
	if r == nil {
		t.Fatalf("ExtractFullEnumRegistry returned nil")
	}
	if !r.IsSharedFieldsEnum("Expr") {
		t.Errorf("Expr should be marked shared-fields")
	}
	if r.IsSharedFieldsEnum("Tree") {
		t.Errorf("Tree should NOT be marked shared-fields")
	}
}

// TestTransformValueEnumSource_SharedFieldsLowering is the end-to-end
// codegen test: feed dingo source containing a shared-fields enum
// through the same entry point the build pipeline uses, and verify the
// emitted Go code matches the Option B layout. Catches regressions in
// the dispatch from TransformValueEnumSource → NewEnumCodeGen → the
// shared-fields layout generator.
func TestTransformValueEnumSource_SharedFieldsLowering(t *testing.T) {
	src := []byte(`package ir

enum Expr {
    shared { pos: Pos, op: Op }
    *AddrExpr { X: Node }
    *NilExpr {}
}`)

	out, _ := TransformValueEnumSource(src, "")
	got := string(out)

	// Option B artefacts must appear.
	for _, want := range []string{
		"type ExprTag uint8",
		"ExprTagAddrExpr ExprTag = iota",
		"ExprTagNilExpr",
		"type ExprVariantData interface { isExprVariantData() }",
		"type Expr struct {",
		"pos Pos",
		"op Op",
		"tag ExprTag",
		"data ExprVariantData",
		"type ExprAddrExprData struct { X Node }",
		"func (*ExprAddrExprData) isExprVariantData() {}",
		"type ExprNilExprData struct {}",
		"func (*ExprNilExprData) isExprVariantData() {}",
		"func NewExprAddrExpr(pos Pos, op Op, X Node) *Expr {",
		"func NewExprNilExpr(pos Pos, op Op) *Expr {",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in transform output.\n%s", want, got)
		}
	}

	// Classic interface artefacts must NOT appear — that would mean the
	// dispatch picked the wrong layout.
	for _, leak := range []string{
		"type Expr interface",
		"isExpr()",
	} {
		if strings.Contains(got, leak) {
			t.Errorf("transform leaked classic layout artefact %q.\n%s", leak, got)
		}
	}

	// Pre-enum source must survive verbatim.
	if !strings.Contains(got, "package ir") {
		t.Errorf("source before enum was lost.\n%s", got)
	}
}

// TestTransformValueEnumSource_ClassicEnum_Unchanged is the negation: a
// classic enum should still go through the original interface lowering.
// Guards against accidental routing of classic enums into the new path.
func TestTransformValueEnumSource_ClassicEnum_Unchanged(t *testing.T) {
	src := []byte(`enum Tree {
    *Node { value: int }
    *Leaf {}
}`)

	out, _ := TransformValueEnumSource(src, "")
	got := string(out)

	for _, want := range []string{
		"type Tree interface { isTree() }",
		"func (*TreeNode) isTree() {}",
		"func (*TreeLeaf) isTree() {}",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("classic layout regressed; missing %q.\n%s", want, got)
		}
	}

	for _, leak := range []string{"TreeTag", "TreeVariantData", "TreeNodeData"} {
		if strings.Contains(got, leak) {
			t.Errorf("classic enum leaked shared-fields artefact %q.\n%s", leak, got)
		}
	}
}

// TestTransformValueEnumSource_Generic_SharedFields verifies the
// generic case end-to-end: a `Tree[T]` with shared field. The transform
// must keep the tag type and marker interface non-generic while threading
// `[T any]` through everything else.
func TestTransformValueEnumSource_Generic_SharedFields(t *testing.T) {
	src := []byte(`enum Tree[T] {
    shared { id: int }
    *Node { value: T }
    *Leaf {}
}`)

	out, _ := TransformValueEnumSource(src, "")
	got := string(out)

	for _, want := range []string{
		"type TreeTag uint8",
		"type TreeVariantData interface { isTreeVariantData() }",
		"type Tree[T any] struct {",
		"id int",
		"type TreeNodeData[T any] struct { value T }",
		"type TreeLeafData[T any] struct {}",
		"func NewTreeNode[T any](id int, value T) *Tree[T] {",
		"func NewTreeLeaf[T any](id int) *Tree[T] {",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q.\n%s", want, got)
		}
	}

	// Non-generic constructs must not have leaked type params.
	for _, leak := range []string{"TreeTag[", "TreeVariantData["} {
		if strings.Contains(got, leak) {
			t.Errorf("non-generic construct leaked type params: %q.\n%s", leak, got)
		}
	}
}

// TestTransformValueEnumSource_SharedFieldsRegistry is a regression
// guard for the bug that motivated this test file: TransformValueEnumSource
// builds a registry of its own (separate from ExtractFullEnumRegistry),
// and an earlier version did NOT call RegisterSharedFieldsEnum from
// inside the sum-type branch. The two registries should agree.
func TestTransformValueEnumSource_SharedFieldsRegistry(t *testing.T) {
	src := []byte(`enum Expr {
    shared { pos: Pos }
    *AddrExpr { X: Node }
}`)

	_, transformReg := TransformValueEnumSource(src, "")
	extractReg := ExtractFullEnumRegistry(src)
	if transformReg == nil || extractReg == nil {
		t.Fatalf("nil registry: transform=%v extract=%v", transformReg, extractReg)
	}

	// Both registries must agree on shared-fields status. Today,
	// ExtractFullEnumRegistry marks the enum but TransformValueEnumSource
	// does not — this test pins the desired behaviour. Adjust the
	// production code to match if it fails.
	if !extractReg.IsSharedFieldsEnum("Expr") {
		t.Errorf("ExtractFullEnumRegistry should mark Expr as shared-fields")
	}
	if !transformReg.IsSharedFieldsEnum("Expr") {
		t.Errorf("TransformValueEnumSource registry should also mark Expr as shared-fields " +
			"(parity with ExtractFullEnumRegistry)")
	}
}
