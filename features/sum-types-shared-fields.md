# Sum Types with Shared Fields (Option B layout)

**Priority:** P1
**Status:** ✅ Implemented (codegen + match + generics)
**Inspiration:** Rust struct-with-enum pattern; problem encountered while migrating Go compiler IR to Dingo.

---

## Problem

The current Dingo enum lowers to an interface + per-variant structs:

```dingo
enum Expr {
    *AddrExpr { pos: Pos, op: Op, X: Node }
    *CallExpr { pos: Pos, op: Op, Fun: Node, Args: Nodes }
}
```
↓
```go
type Expr interface { isExpr() }
type ExprAddrExpr struct { pos Pos; op Op; X Node }
type ExprCallExpr struct { pos Pos; op Op; Fun Node; Args Nodes }
```

Since Go forbids methods on interface types, **every method that operates on the enum must be repeated per-variant**:

```go
func (e *ExprAddrExpr) Pos() Pos { return e.pos }
func (e *ExprCallExpr) Pos() Pos { return e.pos }
// ... 27 variants × ~22 shared methods = ~600 lines of boilerplate
```

For AST-style hierarchies with many shared fields (`pos`, `op`, `typ`, `flags`, …) this explodes.

## Proposal

Allow the enum to declare a **`shared { ... }` block** that lifts common fields onto the enum struct itself. The enum is then lowered to a *struct* (not an interface), so methods can be defined directly on it and shared field access is a direct field read.

```dingo
enum Expr {
    shared { pos: Pos, op: Op, typ: *Type }

    *AddrExpr { X: Node }
    *CallExpr { Fun: Node, Args: Nodes }
}

// methods on Expr work natively, shared fields direct:
func (e *Expr) Pos() Pos          { return e.pos }
func (e *Expr) SetPos(p Pos)      { e.pos = p }
func (e *Expr) Type() *Type       { return e.typ }
```

The `shared { ... }` block must be the **first item** inside the enum body. Its presence triggers the new layout; without it the existing interface layout is used.

## Generated Layout

For the example above the codegen emits:

```go
// Discriminator
type ExprTag uint8
const (
    ExprTagAddrExpr ExprTag = iota
    ExprTagCallExpr
)
func (t ExprTag) String() string {
    switch t {
    case ExprTagAddrExpr: return "AddrExpr"
    case ExprTagCallExpr: return "CallExpr"
    }
    return "ExprTag(?)"
}

// Sealed interface for variant payloads
type ExprVariantData interface { isExprVariantData() }

// The enum: struct with shared fields, tag, and variant data
type Expr struct {
    // shared fields, lifted from `shared { ... }`
    pos Pos
    op  Op
    typ *Type
    // discriminator + payload
    tag  ExprTag
    data ExprVariantData
}

// Per-variant data structs (variant-specific fields only)
type ExprAddrExprData struct { X Node }
func (*ExprAddrExprData) isExprVariantData() {}

type ExprCallExprData struct { Fun Node; Args Nodes }
func (*ExprCallExprData) isExprVariantData() {}

// Constructors take shared + variant-specific args, return *Expr
func NewExprAddrExpr(pos Pos, op Op, typ *Type, X Node) *Expr {
    return &Expr{
        pos: pos, op: op, typ: typ,
        tag: ExprTagAddrExpr,
        data: &ExprAddrExprData{X: X},
    }
}
func NewExprCallExpr(pos Pos, op Op, typ *Type, Fun Node, Args Nodes) *Expr {
    return &Expr{
        pos: pos, op: op, typ: typ,
        tag: ExprTagCallExpr,
        data: &ExprCallExprData{Fun: Fun, Args: Args},
    }
}
```

## Match Semantics

Match expressions on shared-fields enums lower to tag-based dispatch. The codegen detects the new layout via the enum registry and emits:

```dingo
match e {
    AddrExpr(x) => f(x, e.pos)
    NilExpr     => nil
}
```
↓
```go
scrut := e
switch scrut.tag {
case ExprTagAddrExpr:
    d := scrut.data.(*ExprAddrExprData)
    x := d.X
    return f(x, e.pos)   // shared `pos` accessed directly through e
case ExprTagNilExpr:
    return nil
}
```

Pattern bindings extract variant-specific fields by casting `scrut.data` to the matching variant Data struct. Shared fields (`pos`, `op`, …) are accessed directly through the original scrutinee variable — they live on the enum struct, not in `data`, so no cast is needed.

Wildcards (`_`) and variable patterns (`x =>`) work the same as for classic enums. Exhaustiveness check applies (the variants are registered the same way).

## Generics

Generic shared-fields enums are supported on the codegen side:

```dingo
enum Tree[T] {
    shared { id: int }
    *Node { value: T, left: *Tree[T], right: *Tree[T] }
    *Leaf {}
}
```
↓
```go
type TreeTag uint8                              // non-generic
type TreeVariantData interface { isTreeVariantData() }  // non-generic

type Tree[T any] struct {
    id   int
    tag  TreeTag
    data TreeVariantData
}
type TreeNodeData[T any] struct { value T; left *Tree[T]; right *Tree[T] }
type TreeLeafData[T any] struct {}

func (*TreeNodeData[T]) isTreeVariantData() {}
func (*TreeLeafData[T]) isTreeVariantData() {}

func NewTreeNode[T any](id int, value T, left *Tree[T], right *Tree[T]) *Tree[T] {
    return &Tree[T]{id: id, tag: TreeTagNode, data: &TreeNodeData[T]{value: value, left: left, right: right}}
}
```

The tag type and the sealed VariantData marker interface stay non-generic — the tag set is the same regardless of `T`, and the marker method has no parameters that would carry type info. Everything else (enum struct, variant Data structs, constructors) propagates the type-parameter list.

**Known limitation**: match codegen for generic shared-fields enums does not yet inject type arguments into the data cast (would emit `scrut.data.(*TreeNodeData)` instead of `scrut.data.(*TreeNodeData[T])`). The driving use case (Go compiler IR) doesn't need this. Track in a follow-up.

## Trade-offs (vs. current interface layout)

| | Interface (current) | Shared-fields struct (this) |
|---|---|---|
| Methods on enum type | ❌ Go forbids on interfaces | ✅ Native struct methods |
| Shared method body | per-variant repetition | one-line field read |
| Variant-specific accessor | type assert + field | tag-match + interface cast + field |
| Memory per node | ~16 (iface header) + variant struct | ~7 shared fields + ~24 (tag+iface) + variant data |
| Heap allocs per node | 1 | 2 |
| Match exhaustiveness | Go type switch | (deferred) tag-based |

Memory is comparable: shared fields shift from the variant struct to the enum struct. For variants without extra fields (e.g. `NilExpr`) the new layout is **lighter** (no variant struct allocation if `data == nil` is used as a sentinel).

## Out of scope

- Match codegen for shared-fields enums (separate proposal).
- Auto-detection of shared fields when no `shared { ... }` block is provided.
- Tagged-union memory packing (`unsafe`-based).

## Open questions

1. **Method name collisions**: if a user defines `func (e *Expr) X()` *and* a variant has field `X`, the method shadows the field — fine, normal Go rules apply. We don't need special handling.
2. **Constructors on unit variants**: with shared fields, a unit variant still needs all shared-field arguments. We could permit `New<Variant>WithShared(...)` and a shorthand `Zero<Variant>()` that fills shared fields with zero values. Defer until a real use case appears.
3. **Visibility of `data` field**: lowercase keeps it package-private; if a caller needs to inspect `e.data` from outside the package, they must use the public accessor methods (which is the intended pattern anyway).
