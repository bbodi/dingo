# Sum Types (Discriminated Unions)

**Priority:** P0 (Critical - Foundation for type system)
**Status:** ✅ Implemented (Phase 11 - Interface-Based Sum Types)
**Implementation:** `pkg/ast/enum_codegen.go` - generates interface-based pattern
**Community Demand:** ⭐⭐⭐⭐⭐ (996+ 👍 on Go proposal #19412 - HIGHEST)
**Inspiration:** Rust, Swift, TypeScript, Kotlin

---

## Overview

Sum types (also called discriminated unions, tagged unions, or algebraic data types) allow a value to be one of several fixed types, with compile-time enforcement and exhaustive checking. This is the most requested Go feature and foundational for Dingo's type system.

## Motivation

### The Problem in Go

```go
// Go uses empty interface{} for "one of many types"
type Response interface{}

func handleResponse(resp Response) {
    // Unsafe type assertions, no exhaustiveness
    switch v := resp.(type) {
    case SuccessResponse:
        fmt.Println("Success:", v.Data)
    case ErrorResponse:
        fmt.Println("Error:", v.Message)
    // Forgot TimeoutResponse - no compiler warning!
    default:
        fmt.Println("Unknown response")
    }
}

// Or manual tagged struct (verbose, error-prone)
type Response struct {
    Tag string // "success" | "error" | "timeout"
    SuccessData *SuccessResponse
    ErrorData *ErrorResponse
    TimeoutData *TimeoutResponse
}

// Caller must remember to check tag AND correct field
if resp.Tag == "success" && resp.SuccessData != nil {
    // handle success
}
```

**Problems:**
- No type safety (interface{} accepts anything)
- No exhaustiveness checking (easy to forget cases)
- Runtime type assertions can panic
- Verbose workarounds with manual tagging
- Nil pointer bugs when accessing wrong variant

### Research Data

- **Go Proposal #19412** - 996+ 👍 (HIGHEST engagement ever)
- **#54685** - Sigma types (dependent type approach)
- **#57644** - Ian Lance Taylor's proposal (extending generics)
- Considered the logical "next step" after generics
- Go team acknowledges "overlap with interfaces in confusing ways"

---

## Proposed Syntax

### Enum-Style Declaration

```dingo
// Sum type with named variants
enum HttpResponse {
    Ok(body: string),
    NotFound,
    ServerError{code: int, message: string},
    Redirect(url: string)
}

// Generic sum types
enum Result[T, E] {
    Ok(T),
    Err(E)
}

enum Option[T] {
    Some(T),
    None
}
```

### Usage with Pattern Matching

```dingo
func handleResponse(resp: HttpResponse) -> string {
    match resp {
        HttpResponse_Ok => "Success: " + getBody(resp),
        HttpResponse_NotFound => "404 Not Found",
        HttpResponse_ServerError => fmt.Sprintf("Error %d: %s", getCode(resp), getMessage(resp)),
        HttpResponse_Redirect => "Redirecting to " + getUrl(resp)
    }
}

// Compiler enforces exhaustiveness
match resp {
    HttpResponse_Ok => ...,
    HttpResponse_NotFound => ...
    // ERROR: Missing ServerError and Redirect cases
}
```

### Constructing Variants

```dingo
// Creating variants
success := NewHttpResponseOk("Hello, World!")
notFound := NewHttpResponseNotFound()
error := NewHttpResponseServerError(500, "Internal error")
redirect := NewHttpResponseRedirect("https://example.com")

// Type inference
let response: HttpResponse = NewHttpResponseOk("data")
```

---

## Transpilation Strategy

### Go Output (Interface-Based Pattern)

**Design Decision: Why Interface-Based, Not Tagged Struct**

Dingo generates **interface-based sum types** (also known as sealed interfaces) because this is the idiomatic Go pattern used by the standard library and experienced Go developers.

```go
// Transpiled sum type - Interface-based pattern
type HttpResponse interface {
    isHttpResponse() // unexported marker = sealed interface
}

// Each variant is a separate struct type
type HttpResponseOk struct {
    Body string
}
func (HttpResponseOk) isHttpResponse() {}

type HttpResponseNotFound struct{}
func (HttpResponseNotFound) isHttpResponse() {}

type HttpResponseServerError struct {
    Code    int
    Message string
}
func (HttpResponseServerError) isHttpResponse() {}

type HttpResponseRedirect struct {
    Url string
}
func (HttpResponseRedirect) isHttpResponse() {}

// Constructor functions (NewVariant pattern)
func NewHttpResponseOk(body string) HttpResponse {
    return HttpResponseOk{Body: body}
}

func NewHttpResponseNotFound() HttpResponse {
    return HttpResponseNotFound{}
}

func NewHttpResponseServerError(code int, message string) HttpResponse {
    return HttpResponseServerError{Code: code, Message: message}
}

func NewHttpResponseRedirect(url string) HttpResponse {
    return HttpResponseRedirect{Url: url}
}
```

### Pattern Match Transpiles to Type Switch

```go
// match expression → Go type switch (idiomatic Go)
func handleResponse(resp HttpResponse) string {
    switch v := resp.(type) {
    case HttpResponseOk:
        return fmt.Sprintf("Success: %s", v.Body)
    case HttpResponseNotFound:
        return "404 Not Found"
    case HttpResponseServerError:
        return fmt.Sprintf("Error %d: %s", v.Code, v.Message)
    case HttpResponseRedirect:
        return fmt.Sprintf("Redirecting to %s", v.Url)
    default:
        panic("unreachable: unhandled HttpResponse variant")
    }
}
```

---

## Pointer-Stored Variants (`*Variant` Prefix)

By default each variant is **value-stored**: the constructor returns a struct value
boxed in the enum interface, the marker method has a value receiver, and a `match`
arm matches `case T:`. That fits self-contained domains where variants are short-lived
and immutable.

For variants that are large, mutable, or need pointer identity — most importantly
**AST node hierarchies** — prefix the variant with `*` to declare it pointer-stored:

```dingo
enum Tree {
    *Node { value: int, left: Tree, right: Tree }   // pointer-stored
    Leaf                                            // value-stored
}
```

### What `*` changes in the generated Go

| Aspect | Value variant (default) | Pointer variant (`*` prefix) |
|---|---|---|
| Marker receiver | `func (T) isEnum() {}` | `func (*T) isEnum() {}` |
| Constructor return | `return T{...}` | `return &T{...}` |
| `match` arm case | `case T:` | `case *T:` |
| Satisfies enum interface | `T` and `*T` both | only `*T` |
| Mutability through interface | copy each access | shared mutation |

For the `Tree` example above:

```go
type Tree interface { isTree() }

// Pointer-stored Node
type TreeNode struct {
    value int
    left  Tree
    right Tree
}
func (*TreeNode) isTree() {}
func NewTreeNode(value int, left Tree, right Tree) Tree {
    return &TreeNode{value: value, left: left, right: right}
}

// Value-stored Leaf (unchanged)
type TreeLeaf struct{}
func (TreeLeaf) isTree() {}
func NewTreeLeaf() Tree  { return TreeLeaf{} }

// match emits the right case form for each variant:
func depth(t Tree) int {
    switch v := t.(type) {
    case *TreeNode:                    // ← pointer case
        return 1 + max(depth(v.left), depth(v.right))
    case TreeLeaf:                     // ← value case
        return 0
    }
    panic("unreachable: exhaustive match")
}
```

### When to use pointer variants

Use `*Variant` when any of the following apply:

- **The codebase already uses pointers** for the same shape (the entire `go/ast`,
  `go/parser`, and `cmd/compile` tree do this). Without `*Variant`, a `match` arm
  emits `case T:` and never matches a `*T` value stored in the interface — leading
  to silent fall-through to the `panic("unreachable")` arm. With `*Variant` the
  case is `case *T:` and the match works.
- **The variant is large** (many fields, or fields with pointers). Pointer storage
  avoids copying the whole struct each time the interface is read.
- **The variant is built incrementally**, mutating fields after construction
  (e.g. parser populating an AST node). A value variant cannot be mutated through
  the interface — every `v := iface.(T)` is a fresh copy.
- **Multiple references to the same logical node** should share identity
  (`==` comparison, slice membership). Value variants compare by content.

Value variants stay the default because:

- Simpler ownership: no nil-pointer cases on a unit variant.
- Smaller code for short data carriers (`Status`, `Color`, `RGB { r, g, b: int }`).
- Compatible with all existing Dingo examples — adding the feature did not change
  any value-stored semantics.

### Mixing pointer and value variants

The two forms can coexist freely in the same enum (see `Tree` above). Each variant's
storage is decided independently. The match generator looks up each pattern in the
enum registry and emits `case T:` or `case *T:` per arm.

---

## Design Decision: No Is* Methods

### The Question

Should Dingo generate `Is*()` methods on variants for type checking?

```go
// Option A: Generate Is* methods (O(n²) methods)
func (HttpResponseOk) IsOk() bool { return true }
func (HttpResponseOk) IsNotFound() bool { return false }
func (HttpResponseOk) IsServerError() bool { return false }
func (HttpResponseOk) IsRedirect() bool { return false }
// ... repeat for ALL variants = n × n methods
```

### Decision: NO - Use Type Switch/Assertion (Idiomatic Go)

**Dingo does NOT generate Is* methods.** This follows Go idioms.

### Rationale

**1. Standard Library Precedent**

The `go/ast` package has 50+ node types. Zero `Is*()` methods. Developers use type switch:

```go
switch n := node.(type) {
case *ast.FuncDecl:
    fmt.Println("function:", n.Name)
case *ast.IfStmt:
    handleIf(n)
}
```

**2. Type Assertion is Built-In**

Go provides type assertion syntax for single-case checks:

```go
// Idiomatic Go - type assertion
if ok, isOk := resp.(HttpResponseOk); isOk {
    fmt.Println("Body:", ok.Body)
}
```

This is more direct than:
```go
// Non-idiomatic - method that still requires assertion after
if resp.IsOk() {
    ok := resp.(HttpResponseOk) // still need this!
    fmt.Println("Body:", ok.Body)
}
```

**3. O(n²) Bloat**

For 10 variants, Is* methods generate 100 methods (10 × 10).
For 20 variants, that's 400 methods. Unnecessary code bloat.

**4. errors.Is Pattern**

When Go stdlib needs a type-check function, it uses standalone functions:

```go
if errors.Is(err, os.ErrNotExist) { ... }
```

If Dingo users need convenience helpers, we can generate standalone functions:

```go
func IsHttpResponseOk(r HttpResponse) bool {
    _, ok := r.(HttpResponseOk)
    return ok
}
```

This is O(n), not O(n²).

### How Dingo Users Should Check Types

```dingo
// In Dingo source - use match (compiles to type switch)
match resp {
    HttpResponse_Ok => handleSuccess(resp),
    HttpResponse_NotFound => handle404(),
    _ => handleOther()
}
```

```go
// Generated Go - idiomatic type switch
switch v := resp.(type) {
case HttpResponseOk:
    handleSuccess(v)
case HttpResponseNotFound:
    handle404()
default:
    handleOther()
}
```

For single-case checks in user's Go code:

```go
// Use type assertion - this is what Go developers expect
if ok, isOk := resp.(HttpResponseOk); isOk {
    process(ok.Body)
}
```

---

## Why Interface-Based, Not Tagged Struct

### Alternative: Tagged Struct (NOT USED)

```go
// Tagged struct approach (product type - NOT idiomatic)
type HttpResponse struct {
    tag               HttpResponseTag
    okBody            *string           // nil when not Ok
    serverErrorCode   *int              // nil when not ServerError
    serverErrorMessage *string          // nil when not ServerError
    redirectUrl       *string           // nil when not Redirect
}
```

**Problems with tagged struct:**
1. **Memory waste** - all fields allocated even when unused
2. **Not a sum type** - all fields accessible (even if nil)
3. **No compile-time safety** - can access wrong variant's fields
4. **Non-idiomatic** - Go developers don't write code this way

### Interface-Based (USED)

```go
// Interface-based approach (true sum type - idiomatic Go)
type HttpResponse interface { isHttpResponse() }

type HttpResponseOk struct { Body string }
func (HttpResponseOk) isHttpResponse() {}
```

**Benefits:**
1. **True sum type** - only one variant's data exists at a time
2. **Memory efficient** - only active variant allocated
3. **Type-safe** - must type-assert to access fields
4. **Idiomatic** - how go/ast, protobuf, and stdlib implement sum types

---

## Implementation Details

### Type System Integration

```dingo
// Sum types are closed (fixed set of variants)
enum Shape {
    Circle(radius: float),
    Rectangle{width: float, height: float},
    Point
}

// Cannot extend externally (unlike interfaces)
// This enables exhaustiveness checking

// Methods on sum types
impl Shape {
    func area() -> float {
        match self {
            Shape_Circle => 3.14 * getRadius(self) * getRadius(self),
            Shape_Rectangle => getWidth(self) * getHeight(self),
            Shape_Point => 0.0
        }
    }
}
```

### Generic Sum Types

```dingo
// Result and Option are generic sum types
enum Result[T, E] {
    Ok(T),
    Err(E)
}

// Instantiation
let success: Result[User, DbError] = NewResultOk(user)
let failure: Result[User, DbError] = NewResultErr(DbError.notFound())

// Nested generics
let nested: Option[Result[User, Error]] = NewOptionSome(NewResultOk(user))
```

---

## Benefits

### Type Safety

```dingo
// ❌ Cannot construct invalid variants
response := NewHttpResponseOk("data", 404)  // ERROR: Ok takes only 1 argument

// ✅ Type-safe access via pattern matching
match response {
    HttpResponse_ServerError => {
        // v is typed as HttpResponseServerError
        // can access v.Code and v.Message safely
    }
}
```

### Exhaustiveness

```dingo
// Compiler tracks which cases are handled
enum Status { Pending, Approved, Rejected }

// ❌ Compile error (future: currently runtime panic)
match status {
    Status_Pending => "waiting",
    Status_Approved => "done"
    // ERROR: Rejected not handled
}

// ✅ Compiles
match status {
    Status_Pending => "waiting",
    Status_Approved => "done",
    Status_Rejected => "rejected"
}

// ✅ Or use wildcard
match status {
    Status_Pending => "waiting",
    _ => "other"
}
```

### Self-Documenting

```dingo
// API contract is clear from type
func fetchUser(id: string) -> Result[User, FetchError]

// Compared to Go
func fetchUser(id string) (*User, error)  // What errors? Can user be nil?
```

---

## Tradeoffs

### Advantages
- ✅ **Eliminates entire classes of bugs** (exhaustiveness prevents forgetting cases)
- ✅ **Type-safe variant access** (cannot access wrong variant's data)
- ✅ **Self-documenting** (type shows all possible cases)
- ✅ **Enables powerful patterns** (Result, Option, state machines)
- ✅ **Idiomatic Go output** (interface + type switch pattern)

### Potential Concerns
- ❓ **Qualified pattern names** (`Status_Pending` instead of bare `Pending`)
  - *Mitigation:* Future work on symbol table will enable natural syntax
- ❓ **Learning curve** (new concept for Go developers)
  - *Mitigation:* Generated code is idiomatic Go, easy to understand

---

## Examples

### Example 1: JSON Value

```dingo
enum JsonValue {
    Null,
    Bool(bool),
    Number(float64),
    String(string),
    Array([]JsonValue),
    Object(map[string]JsonValue)
}

func stringify(value: JsonValue) -> string {
    match value {
        JsonValue_Null => "null",
        JsonValue_Bool => if getBool(value) { "true" } else { "false" },
        JsonValue_Number => fmt.Sprint(getNumber(value)),
        JsonValue_String => fmt.Sprintf("%q", getString(value)),
        JsonValue_Array => "[" + stringifyArray(getArray(value)) + "]",
        JsonValue_Object => "{" + stringifyObject(getObject(value)) + "}"
    }
}
```

### Example 2: State Machine

```dingo
enum ConnectionState {
    Disconnected,
    Connecting{attempt: int},
    Connected{session: Session},
    Error{error: string, retryAfter: time.Duration}
}

func handleState(state: ConnectionState) {
    match state {
        ConnectionState_Disconnected => startConnection(),
        ConnectionState_Connecting => showProgress(state),
        ConnectionState_Connected => useSession(state),
        ConnectionState_Error => scheduleRetry(state)
    }
}
```

---

## Success Criteria

- [x] Sum types generate interface-based pattern (true sum types)
- [x] Pattern matching compiles to type switch
- [x] Transpiled code is idiomatic Go
- [ ] Exhaustiveness checking catches missing cases at compile-time
- [ ] Natural pattern syntax (Pending instead of Status_Pending)
- [x] No O(n²) method explosion - use type assertion

---

## References

- Go Proposal #19412: Sum types (996+ 👍)
- Go Proposal #54685: Sigma types
- Go Proposal #57644: Extending generics with unions (Ian Lance Taylor)
- Rust Enums: https://doc.rust-lang.org/book/ch06-00-enums.html
- Swift Enums: https://docs.swift.org/swift-book/documentation/the-swift-programming-language/enumerations/
- TypeScript Discriminated Unions: https://www.typescriptlang.org/docs/handbook/unions-and-intersections.html

---

## Implementation History

- **Phase 10**: Initial enum support with tagged-struct pattern
- **Phase 11**: Refactored to interface-based sum types
  - Removed O(n²) Is* methods
  - Match expressions use type switch
  - Constructor naming: NewVariant() pattern
  - Documentation updated with design rationale
