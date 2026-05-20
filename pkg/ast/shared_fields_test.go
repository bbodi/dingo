package ast

import (
	"strings"
	"testing"
)

// TestEnumParser_SharedFields_BlockParsed verifies that a `shared { ... }`
// block at the top of an enum body is parsed into EnumDecl.SharedFields and
// not mistaken for a variant.
func TestEnumParser_SharedFields_BlockParsed(t *testing.T) {
	src := `enum Expr {
    shared { pos: Pos, op: Op }
    *AddrExpr { X: Node }
    *CallExpr { Fun: Node, Args: Nodes }
}`
	p := NewEnumParser([]byte(src), 0)
	decl, _, err := p.ParseEnumDecl()
	if err != nil {
		t.Fatalf("ParseEnumDecl: %v", err)
	}

	if !decl.HasSharedFields() {
		t.Fatalf("expected HasSharedFields() to be true")
	}
	if len(decl.SharedFields) != 2 {
		t.Fatalf("expected 2 shared fields, got %d", len(decl.SharedFields))
	}
	if got, want := decl.SharedFields[0].Name.Name, "pos"; got != want {
		t.Errorf("shared[0].Name = %q, want %q", got, want)
	}
	if got, want := decl.SharedFields[1].Name.Name, "op"; got != want {
		t.Errorf("shared[1].Name = %q, want %q", got, want)
	}
	if len(decl.Variants) != 2 {
		t.Fatalf("expected 2 variants, got %d", len(decl.Variants))
	}
	if got, want := decl.Variants[0].Name.Name, "AddrExpr"; got != want {
		t.Errorf("variants[0].Name = %q, want %q", got, want)
	}
}

// TestEnumParser_NoSharedBlock_Backcompat verifies that classic enums
// (without a shared block) continue to parse with SharedFields nil and
// HasSharedFields false — i.e. the new syntax is opt-in.
func TestEnumParser_NoSharedBlock_Backcompat(t *testing.T) {
	src := `enum Tree { *Node { value: int }, Leaf }`
	p := NewEnumParser([]byte(src), 0)
	decl, _, err := p.ParseEnumDecl()
	if err != nil {
		t.Fatalf("ParseEnumDecl: %v", err)
	}
	if decl.HasSharedFields() {
		t.Errorf("expected HasSharedFields() to be false for classic enum")
	}
	if decl.SharedFields != nil {
		t.Errorf("expected SharedFields to be nil, got %v", decl.SharedFields)
	}
	if len(decl.Variants) != 2 {
		t.Fatalf("expected 2 variants, got %d", len(decl.Variants))
	}
}

// TestEnumCodeGen_SharedFields_Layout covers the Option B struct layout:
// the enum lowers to a struct with shared fields + tag + data interface,
// each variant gets a thin Data struct, and constructors take both shared
// and variant-specific args.
func TestEnumCodeGen_SharedFields_Layout(t *testing.T) {
	src := `enum Expr {
    shared { pos: Pos, op: Op }
    *AddrExpr { X: Node }
    *CallExpr { Fun: Node, Args: Nodes }
}`
	p := NewEnumParser([]byte(src), 0)
	decl, _, err := p.ParseEnumDecl()
	if err != nil {
		t.Fatalf("ParseEnumDecl: %v", err)
	}

	got := string(NewEnumCodeGen().Generate(decl, "", 0, 0))

	mustContain := []string{
		// tag type + iota constants
		"type ExprTag uint8",
		"ExprTagAddrExpr ExprTag = iota",
		"ExprTagCallExpr",
		// tag String() for diagnostics
		"func (t ExprTag) String() string {",
		`case ExprTagAddrExpr: return "AddrExpr"`,
		// sealed variant-data interface
		"type ExprVariantData interface { isExprVariantData() }",
		// enum struct with shared fields + tag + data
		"type Expr struct {",
		"pos Pos",
		"op Op",
		"tag ExprTag",
		"data ExprVariantData",
		// Tag() accessor on the enum struct
		"func (e *Expr) Tag() ExprTag { return e.tag }",
		// per-variant Data structs (variant-specific fields only — pos/op NOT here)
		"type ExprAddrExprData struct { X Node }",
		"func (*ExprAddrExprData) isExprVariantData() {}",
		"type ExprCallExprData struct { Fun Node; Args Nodes }",
		"func (*ExprCallExprData) isExprVariantData() {}",
		// constructors take shared + variant args, return *Expr
		"func NewExprAddrExpr(pos Pos, op Op, X Node) *Expr {",
		"return &Expr{pos: pos, op: op, tag: ExprTagAddrExpr, data: &ExprAddrExprData{X: X}}",
		"func NewExprCallExpr(pos Pos, op Op, Fun Node, Args Nodes) *Expr {",
	}
	for _, s := range mustContain {
		if !strings.Contains(got, s) {
			t.Errorf("generated code missing %q.\nFull output:\n%s", s, got)
		}
	}

	// Shared-fields layout must NOT emit the classic interface form.
	mustNotContain := []string{
		"type Expr interface",
		"isExpr()",
	}
	for _, s := range mustNotContain {
		if strings.Contains(got, s) {
			t.Errorf("generated code unexpectedly contains %q (should use shared-fields layout, not interface).\nFull output:\n%s", s, got)
		}
	}
}

// TestEnumCodeGen_SharedFields_NoVariantFields verifies a variant with
// only shared state (no variant-specific fields, e.g. `NilExpr` in the
// Go IR) emits an empty Data struct and a constructor that only takes
// the shared fields.
func TestEnumCodeGen_SharedFields_NoVariantFields(t *testing.T) {
	src := `enum Expr {
    shared { pos: Pos }
    *NilExpr {}
}`
	p := NewEnumParser([]byte(src), 0)
	decl, _, err := p.ParseEnumDecl()
	if err != nil {
		t.Fatalf("ParseEnumDecl: %v", err)
	}
	got := string(NewEnumCodeGen().Generate(decl, "", 0, 0))

	mustContain := []string{
		"type ExprNilExprData struct {}",
		"func NewExprNilExpr(pos Pos) *Expr {",
		"return &Expr{pos: pos, tag: ExprTagNilExpr, data: &ExprNilExprData{}}",
	}
	for _, s := range mustContain {
		if !strings.Contains(got, s) {
			t.Errorf("generated code missing %q.\nFull output:\n%s", s, got)
		}
	}
}

// TestEnumCodeGen_SharedFields_Generic verifies that shared-fields enums
// declared with type parameters propagate them through the enum struct,
// each variant Data struct, marker method, and constructor — while the
// tag type and VariantData marker interface stay non-generic.
func TestEnumCodeGen_SharedFields_Generic(t *testing.T) {
	src := `enum Tree[T] {
    shared { id: int }
    *Node { value: T, left: *Tree[T], right: *Tree[T] }
    *Leaf {}
}`
	p := NewEnumParser([]byte(src), 0)
	decl, _, err := p.ParseEnumDecl()
	if err != nil {
		t.Fatalf("ParseEnumDecl: %v", err)
	}
	got := string(NewEnumCodeGen().Generate(decl, "", 0, 0))

	mustContain := []string{
		// Tag + marker interface stay non-generic.
		"type TreeTag uint8",
		"type TreeVariantData interface { isTreeVariantData() }",
		// Enum struct and variant Data structs are generic.
		"type Tree[T any] struct {",
		"type TreeNodeData[T any] struct { value T; left *Tree[T]; right *Tree[T] }",
		"type TreeLeafData[T any] struct {}",
		// Marker methods carry bare type args on the receiver.
		"func (*TreeNodeData[T]) isTreeVariantData() {}",
		"func (*TreeLeafData[T]) isTreeVariantData() {}",
		// Tag() accessor on the generic enum struct.
		"func (e *Tree[T]) Tag() TreeTag",
		// Constructor: type params on function name; bare args at instantiation.
		"func NewTreeNode[T any](id int, value T, left *Tree[T], right *Tree[T]) *Tree[T] {",
		"return &Tree[T]{id: id, tag: TreeTagNode, data: &TreeNodeData[T]{value: value, left: left, right: right}}",
		"func NewTreeLeaf[T any](id int) *Tree[T] {",
		"return &Tree[T]{id: id, tag: TreeTagLeaf, data: &TreeLeafData[T]{}}",
	}
	for _, s := range mustContain {
		if !strings.Contains(got, s) {
			t.Errorf("generated code missing %q.\nFull output:\n%s", s, got)
		}
	}

	// Tag constants stay non-generic.
	if strings.Contains(got, "TreeTagNode[T]") || strings.Contains(got, "TreeTag[T]") {
		t.Errorf("tag constants leaked type parameters.\n%s", got)
	}
}

// TestEnumParser_SharedFields_SingleField is a minimal-case probe: one
// shared field, one variant. Catches off-by-one issues in
// parseStructFields' handling of comma-less single-field blocks.
func TestEnumParser_SharedFields_SingleField(t *testing.T) {
	src := `enum E { shared { pos: Pos } *V { x: int } }`
	p := NewEnumParser([]byte(src), 0)
	decl, _, err := p.ParseEnumDecl()
	if err != nil {
		t.Fatalf("ParseEnumDecl: %v", err)
	}
	if !decl.HasSharedFields() {
		t.Fatalf("expected HasSharedFields() true")
	}
	if len(decl.SharedFields) != 1 {
		t.Fatalf("expected 1 shared field, got %d", len(decl.SharedFields))
	}
	if got, want := decl.SharedFields[0].Name.Name, "pos"; got != want {
		t.Errorf("shared[0].Name = %q, want %q", got, want)
	}
	if got, want := decl.SharedFields[0].Type.Text, "Pos"; got != want {
		t.Errorf("shared[0].Type.Text = %q, want %q", got, want)
	}
}

// TestEnumParser_SharedFields_ComplexTypes verifies that the supported
// type expressions are captured verbatim into EnumField.Type.Text.
// Supported here: qualified names (`pkg.T`), pointers (`*T`, `**T`),
// slice (`[]T`), and slice-of-pointer (`[]*T`). Maps and function types
// are out of scope of parseTypeExpr at the moment — covered elsewhere.
func TestEnumParser_SharedFields_ComplexTypes(t *testing.T) {
	src := `enum Expr {
    shared {
        pos: src.XPos,
        typ: *Type,
        parent: **Node,
        children: []Node,
        defs: []*Func,
    }
    *Call { Fun: Node }
}`
	p := NewEnumParser([]byte(src), 0)
	decl, _, err := p.ParseEnumDecl()
	if err != nil {
		t.Fatalf("ParseEnumDecl: %v", err)
	}
	if got, want := len(decl.SharedFields), 5; got != want {
		t.Fatalf("expected %d shared fields, got %d", want, got)
	}
	wantTypes := map[string]string{
		"pos":      "src.XPos",
		"typ":      "*Type",
		"parent":   "**Node",
		"children": "[]Node",
		"defs":     "[]*Func",
	}
	for _, f := range decl.SharedFields {
		want, ok := wantTypes[f.Name.Name]
		if !ok {
			t.Errorf("unexpected shared field %q", f.Name.Name)
			continue
		}
		if got := f.Type.Text; got != want {
			t.Errorf("shared %q: Type.Text = %q, want %q", f.Name.Name, got, want)
		}
	}

	// Verify the verbatim text round-trips into the generated Go code.
	got := string(NewEnumCodeGen().Generate(decl, "", 0, 0))
	for _, want := range []string{
		"pos src.XPos",
		"typ *Type",
		"parent **Node",
		"children []Node",
		"defs []*Func",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing field in generated struct: %q.\n%s", want, got)
		}
	}
}

// TestEnumParser_SharedFields_MultiTypeParams verifies that a generic
// enum with multiple type parameters parses fine alongside a shared block
// — the order is `enum Name[K, V] { shared {...} ... }`.
func TestEnumParser_SharedFields_MultiTypeParams(t *testing.T) {
	src := `enum Entry[K, V] {
    shared { hash: uint64 }
    *Used { key: K, value: V }
    *Empty {}
}`
	p := NewEnumParser([]byte(src), 0)
	decl, _, err := p.ParseEnumDecl()
	if err != nil {
		t.Fatalf("ParseEnumDecl: %v", err)
	}
	if decl.TypeParams == nil || len(decl.TypeParams.Params) != 2 {
		t.Fatalf("expected 2 type params, got %#v", decl.TypeParams)
	}
	if got, want := decl.TypeParams.Params[0].Name, "K"; got != want {
		t.Errorf("TypeParams[0] = %q, want %q", got, want)
	}
	if got, want := decl.TypeParams.Params[1].Name, "V"; got != want {
		t.Errorf("TypeParams[1] = %q, want %q", got, want)
	}
	if !decl.HasSharedFields() {
		t.Fatalf("expected shared fields to be parsed alongside generics")
	}
}

// TestEnumParser_NoSharedBlock_VariantNamedShared verifies that the
// `shared` keyword is only recognised as the leading block; if a real
// variant happens to be named `shared`, it should still be treated as a
// variant. (This is a contrived case, but the detection logic uses a
// keyword match, so we want to be sure it doesn't fire mid-body.)
//
// We test the *negation*: a variant whose name doesn't start with `shared`
// followed by `{` must not trigger the shared layout. The case where a
// real variant is literally named "shared" is left undefined — users
// should avoid that.
func TestEnumParser_NoSharedBlock_VariantOnly(t *testing.T) {
	src := `enum E {
    *Foo { x: int }
    *Bar { y: int }
}`
	p := NewEnumParser([]byte(src), 0)
	decl, _, err := p.ParseEnumDecl()
	if err != nil {
		t.Fatalf("ParseEnumDecl: %v", err)
	}
	if decl.HasSharedFields() {
		t.Errorf("expected HasSharedFields() false; got SharedFields=%v", decl.SharedFields)
	}
	if got, want := len(decl.Variants), 2; got != want {
		t.Errorf("Variants = %d, want %d", got, want)
	}
}

// TestEnumCodeGen_TagIota_OnlyFirstHasInit guards the `iota` constant
// emission: only the first tag constant should have `= iota`; the rest
// should appear bare so Go's iota mechanism increments them.
func TestEnumCodeGen_TagIota_OnlyFirstHasInit(t *testing.T) {
	src := `enum E {
    shared { pos: Pos }
    *A {}
    *B {}
    *C {}
}`
	p := NewEnumParser([]byte(src), 0)
	decl, _, err := p.ParseEnumDecl()
	if err != nil {
		t.Fatalf("ParseEnumDecl: %v", err)
	}
	got := string(NewEnumCodeGen().Generate(decl, "", 0, 0))

	if !strings.Contains(got, "ETagA ETag = iota") {
		t.Errorf("first tag missing `= iota` init.\n%s", got)
	}
	// Subsequent tags must appear without their own init expression.
	for _, bare := range []string{"\n\tETagB\n", "\n\tETagC\n"} {
		if !strings.Contains(got, bare) {
			t.Errorf("subsequent tag should be bare %q.\n%s", bare, got)
		}
	}
	// Only one `= iota` total — the rest are not allowed to re-init.
	if n := strings.Count(got, "= iota"); n != 1 {
		t.Errorf("expected exactly one `= iota`, got %d.\n%s", n, got)
	}
}

// TestEnumCodeGen_TagString_AllVariantsAndFallback verifies that
// (ETag).String() returns the variant name for each tag and falls back
// to a sentinel for unknown values.
func TestEnumCodeGen_TagString_AllVariantsAndFallback(t *testing.T) {
	src := `enum E {
    shared { pos: Pos }
    *A {}
    *B {}
}`
	p := NewEnumParser([]byte(src), 0)
	decl, _, err := p.ParseEnumDecl()
	if err != nil {
		t.Fatalf("ParseEnumDecl: %v", err)
	}
	got := string(NewEnumCodeGen().Generate(decl, "", 0, 0))

	for _, want := range []string{
		`case ETagA: return "A"`,
		`case ETagB: return "B"`,
		`return "ETag(?)"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in tag String().\n%s", want, got)
		}
	}
}

// TestEnumCodeGen_MarkerMethodOnEveryVariant guards a structural
// invariant: each variant's Data struct must implement the sealed
// marker interface; otherwise it cannot be assigned to enum.data.
func TestEnumCodeGen_MarkerMethodOnEveryVariant(t *testing.T) {
	src := `enum E {
    shared { pos: Pos }
    *A { x: int }
    *B { y: int }
    *C {}
}`
	p := NewEnumParser([]byte(src), 0)
	decl, _, err := p.ParseEnumDecl()
	if err != nil {
		t.Fatalf("ParseEnumDecl: %v", err)
	}
	got := string(NewEnumCodeGen().Generate(decl, "", 0, 0))

	for _, v := range []string{"A", "B", "C"} {
		want := "func (*E" + v + "Data) isEVariantData() {}"
		if !strings.Contains(got, want) {
			t.Errorf("missing marker method for variant %q: want %q.\n%s", v, want, got)
		}
	}
}

// TestEnumCodeGen_Constructor_SharedBeforeVariantArgs is a contract test:
// the constructor's parameter list must list all shared fields first, in
// declaration order, followed by the variant-specific fields. Match
// codegen does not depend on this, but the call sites do.
func TestEnumCodeGen_Constructor_SharedBeforeVariantArgs(t *testing.T) {
	src := `enum E {
    shared { pos: Pos, op: Op }
    *Call { Fun: Node, Args: Nodes }
}`
	p := NewEnumParser([]byte(src), 0)
	decl, _, err := p.ParseEnumDecl()
	if err != nil {
		t.Fatalf("ParseEnumDecl: %v", err)
	}
	got := string(NewEnumCodeGen().Generate(decl, "", 0, 0))

	want := "func NewECall(pos Pos, op Op, Fun Node, Args Nodes) *E {"
	if !strings.Contains(got, want) {
		t.Errorf("constructor signature wrong; want %q.\n%s", want, got)
	}
	// Verify the body wires shared fields onto the struct and variant
	// fields onto the Data struct.
	wantBody := "return &E{pos: pos, op: op, tag: ETagCall, data: &ECallData{Fun: Fun, Args: Args}}"
	if !strings.Contains(got, wantBody) {
		t.Errorf("constructor body wrong; want %q.\n%s", wantBody, got)
	}
}

// TestEnumCodeGen_TupleVariant verifies that a tuple-style variant
// (positional fields, no names) still gets variant Data with the
// canonical Value/Value0/... naming, and the constructor uses
// value/value0/... for parameters.
func TestEnumCodeGen_TupleVariant_SharedFields(t *testing.T) {
	src := `enum Op {
    shared { kind: int }
    *Bin(int, int)
    *Un(int)
}`
	p := NewEnumParser([]byte(src), 0)
	decl, _, err := p.ParseEnumDecl()
	if err != nil {
		t.Fatalf("ParseEnumDecl: %v", err)
	}
	got := string(NewEnumCodeGen().Generate(decl, "", 0, 0))

	for _, want := range []string{
		"type OpBinData struct { Value0 int; Value1 int }",
		"type OpUnData struct { Value int }",
		"func NewOpBin(kind int, value0 int, value1 int) *Op {",
		"func NewOpUn(kind int, value int) *Op {",
		"return &Op{kind: kind, tag: OpTagBin, data: &OpBinData{Value0: value0, Value1: value1}}",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q.\n%s", want, got)
		}
	}
}

// TestEnumCodeGen_Generic_MultiTypeParam_SharedFields combines several
// stress points: two type params, the shared block uses one of them,
// and at least one variant carries a non-trivial generic field type.
// Validates that type-param threading is consistent end-to-end.
func TestEnumCodeGen_Generic_MultiTypeParam_SharedFields(t *testing.T) {
	src := `enum Entry[K, V] {
    shared { hash: uint64 }
    *Used { key: K, value: V }
    *Tombstone {}
}`
	p := NewEnumParser([]byte(src), 0)
	decl, _, err := p.ParseEnumDecl()
	if err != nil {
		t.Fatalf("ParseEnumDecl: %v", err)
	}
	got := string(NewEnumCodeGen().Generate(decl, "", 0, 0))

	mustContain := []string{
		// Tag and marker iface remain non-generic.
		"type EntryTag uint8",
		"type EntryVariantData interface { isEntryVariantData() }",
		// Enum struct carries both type params, plus the shared field.
		"type Entry[K, V any] struct {",
		"hash uint64",
		// Variant Data structs propagate both type params.
		"type EntryUsedData[K, V any] struct { key K; value V }",
		"type EntryTombstoneData[K, V any] struct {}",
		// Marker methods on both Data structs.
		"func (*EntryUsedData[K, V]) isEntryVariantData() {}",
		"func (*EntryTombstoneData[K, V]) isEntryVariantData() {}",
		// Tag() receiver carries the bare instantiation form.
		"func (e *Entry[K, V]) Tag() EntryTag",
		// Constructors thread params on the function and bare args inside.
		"func NewEntryUsed[K, V any](hash uint64, key K, value V) *Entry[K, V] {",
		"return &Entry[K, V]{hash: hash, tag: EntryTagUsed, data: &EntryUsedData[K, V]{key: key, value: value}}",
		"func NewEntryTombstone[K, V any](hash uint64) *Entry[K, V] {",
		"return &Entry[K, V]{hash: hash, tag: EntryTagTombstone, data: &EntryTombstoneData[K, V]{}}",
	}
	for _, s := range mustContain {
		if !strings.Contains(got, s) {
			t.Errorf("missing %q.\nFull output:\n%s", s, got)
		}
	}

	// Tag constants must not leak type params.
	for _, leak := range []string{"EntryTagUsed[K", "EntryTag[K", "EntryTagTombstone["} {
		if strings.Contains(got, leak) {
			t.Errorf("tag constants leaked type parameters: %q.\n%s", leak, got)
		}
	}
}

// TestEnumCodeGen_ClassicLayout_Unchanged is a guard: enums without a
// `shared { ... }` block continue to emit the original interface-based
// layout. This prevents accidental breakage of every existing dingo enum.
func TestEnumCodeGen_ClassicLayout_Unchanged(t *testing.T) {
	src := `enum Tree { *Node { value: int }, Leaf }`
	p := NewEnumParser([]byte(src), 0)
	decl, _, err := p.ParseEnumDecl()
	if err != nil {
		t.Fatalf("ParseEnumDecl: %v", err)
	}
	got := string(NewEnumCodeGen().Generate(decl, "", 0, 0))

	mustContain := []string{
		"type Tree interface { isTree() }",
		"func (*TreeNode) isTree() {}",
		"NewTreeNode(value int) Tree { return &TreeNode{value: value} }",
	}
	for _, s := range mustContain {
		if !strings.Contains(got, s) {
			t.Errorf("classic layout regressed; missing %q.\nFull output:\n%s", s, got)
		}
	}
	// Shared-fields-only artefacts must NOT appear.
	for _, s := range []string{"TreeTag", "TreeVariantData", "TreeNodeData"} {
		if strings.Contains(got, s) {
			t.Errorf("classic layout leaked shared-fields artefact %q.\nFull output:\n%s", s, got)
		}
	}
}
