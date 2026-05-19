# Pattern Matching

**Priority:** P0 (Critical - Core MVP Feature)
**Status:** ✅ Implemented (Phase 10 - Token-Based Parser)
**Implementation:** `pkg/goparser/parser/parser.go` - `transformMatch()`
**Community Demand:** ⭐⭐⭐⭐⭐ (Essential for sum types and enums)
**Inspiration:** Rust, Swift, Kotlin

---

## Overview

Pattern matching provides exhaustive, compile-time checked handling of discriminated unions, enums, and other data structures. The `match` expression replaces Go's limited `switch` statement with powerful destructuring and type-safe branching.

## Motivation

### The Problem in Go

```go
// Go's switch is limited - no exhaustiveness checking
func handleResponse(resp interface{}) string {
    switch v := resp.(type) {
    case SuccessResponse:
        return fmt.Sprintf("Success: %s", v.Data)
    case ErrorResponse:
        return fmt.Sprintf("Error: %s", v.Message)
    // Forgot TimeoutResponse case - NO COMPILER WARNING
    default:
        return "Unknown"
    }
}

// Type assertions are unsafe
func process(value interface{}) {
    if user, ok := value.(*User); ok {
        // Handle user
    } else if order, ok := value.(*Order); ok {
        // Handle order
    } else {
        // Default case
    }
}
```

**Problems:**
- No compile-time exhaustiveness checking
- Easy to forget cases (silent bugs)
- Verbose type assertions with fallbacks
- Can't destructure complex types

### Research Data

- **Proposal #45346** - Pattern matching for sum types
- **Kotlin when + sealed classes** = developer favorite
- **Rust match** rated as essential feature (98% approval)

---

## Proposed Syntax

### Basic Match Expression

```dingo
// Match on enum/sum type
func handleResult(result: Result[User, Error]) -> string {
    match result {
        Ok(user) => "Found user: ${user.name}",
        Err(error) => "Error: ${error.message}"
    }
}

// Match with block bodies
func process(option: Option[int]) -> int {
    match option {
        Some(value) => {
            println("Got value: ${value}")
            return value * 2
        },
        None => {
            println("No value")
            return 0
        }
    }
}
```

### Destructuring

```dingo
// Destructure structs
match user {
    User{name: "Alice", age: 30} => "Exact match",
    User{name: "Bob", ..} => "Bob with any age",
    User{age, ..} => "Age is ${age}"
}

// Destructure tuples
match point {
    (0, 0) => "Origin",
    (x, 0) => "On X axis at ${x}",
    (0, y) => "On Y axis at ${y}",
    (x, y) => "Point at (${x}, ${y})"
}
```

### Guards

```dingo
match value {
    Some(x) if x > 10 => "Large value",
    Some(x) if x > 0 => "Small value",
    Some(x) => "Non-positive",
    None => "No value"
}
```

### Nested Patterns

```dingo
match response {
    Ok(User{address: Some(Address{city: "NYC"})}) => "NYC user",
    Ok(User{address: Some(_)}) => "User with address",
    Ok(User{address: None}) => "User without address",
    Err(_) => "Error occurred"
}
```

---

## Transpilation Strategy

### Simple Match

```dingo
// Dingo source
match result {
    Ok(user) => handleUser(user),
    Err(error) => handleError(error)
}
```

```go
// Transpiled Go
switch {
case __result.value != nil && __result.err == nil:
    user := *__result.value
    handleUser(user)
case __result.err != nil:
    error := __result.err
    handleError(error)
}
```

### With Exhaustiveness Checking

```dingo
// Dingo source (compiler enforces all cases)
enum Status { Pending, Approved, Rejected }

match status {
    Pending => "waiting",
    Approved => "accepted"
    // ERROR: Missing Rejected case
}
```

```go
// Transpiled with panic for unreachable default
switch status {
case StatusPending:
    return "waiting"
case StatusApproved:
    return "accepted"
case StatusRejected:
    panic("unreachable: Rejected case not handled in source")
}
```

### With Guards

```dingo
// Dingo source
match value {
    Some(x) if x > 10 => "large",
    Some(x) => "small",
    None => "none"
}
```

```go
// Transpiled Go
switch {
case opt.isSet && *opt.value > 10:
    return "large"
case opt.isSet:
    return "small"
case !opt.isSet:
    return "none"
}
```

### On Pointer-Stored Variants

When the enum declares a variant with the `*Variant` prefix
(see [sum-types.md](sum-types.md#pointer-stored-variants-variant-prefix))
the match generator emits `case *T:` for that arm instead of `case T:`.
Bindings work the same way because Go auto-dereferences through the
pointer:

```dingo
enum Tree {
    *Node { value: int, left: Tree, right: Tree }
    Leaf
}

match t {
    Node(_, left, right) => 1 + max(depth(left), depth(right))
    Leaf => 0
}
```

```go
// Transpiled Go
switch v := t.(type) {
case *TreeNode:                     // ← pointer case for *Node variant
    left := v.left                  // v is *TreeNode; v.left auto-derefs
    right := v.right
    return 1 + max(depth(left), depth(right))
case TreeLeaf:                      // ← value case for unit variant
    return 0
}
panic("unreachable: exhaustive match")
```

This is the form a `match` needs to interoperate with codebases that
store AST nodes by pointer (the entire `go/ast` and `go/parser`
ecosystem, plus `cmd/compile`). Without the `*` prefix, the generated
`case T:` never matches a `*T` value in the interface and every call
falls through to the `panic` arm.

---

## Inspiration from Other Languages

### Rust's Match Expression

```rust
match result {
    Ok(value) => println!("Got: {}", value),
    Err(e) => println!("Error: {}", e),
}

// Exhaustiveness is enforced
enum Message {
    Quit,
    Move { x: i32, y: i32 },
    Write(String),
}

match msg {
    Message::Quit => quit(),
    Message::Move { x, y } => move_to(x, y),
    Message::Write(text) => write(text),
    // Compiler error if any variant is missing
}

// With guards
match number {
    n if n < 0 => println!("negative"),
    0 => println!("zero"),
    n if n > 0 => println!("positive"),
    _ => unreachable!(),
}
```

**Key Insights:**
- **Exhaustiveness checking** prevents missing cases
- **Value extraction** with pattern binding
- **Guards** for conditional matching
- **Expression-based** (returns a value)

### Kotlin's When Expression

```kotlin
when (result) {
    is Success -> println("Success: ${result.data}")
    is Error -> println("Error: ${result.message}")
}

// Sealed classes enable exhaustiveness
sealed class Result {
    data class Success(val data: String) : Result()
    data class Error(val message: String) : Result()
}

when (result) {
    is Success -> handleSuccess(result.data)
    is Error -> handleError(result.message)
    // No else needed - compiler knows all cases covered
}

// With guards
when {
    x in 1..10 -> "small"
    x in 11..100 -> "medium"
    else -> "large"
}
```

**Key Insights:**
- **Smart casts** (auto-cast after type check)
- **Sealed classes** enable exhaustiveness
- **When as expression** (can assign result)
- **Flexible conditions** (not just equality)

### Swift's Switch with Pattern Matching

```swift
switch result {
case .success(let value):
    print("Success: \(value)")
case .failure(let error):
    print("Error: \(error)")
}

// Enum with associated values
enum Result[T, E] {
    case success(T)
    case failure(E)
}

// Pattern matching with where clauses
switch point {
case (0, 0):
    print("origin")
case (let x, 0):
    print("on x-axis at \(x)")
case (0, let y):
    print("on y-axis at \(y)")
case (let x, let y) where x == y:
    print("on diagonal")
case let (x, y):
    print("at (\(x), \(y))")
}
```

**Key Insights:**
- **Associated values** for rich enums
- **Where clauses** for guards
- **Let bindings** for value extraction
- **Exhaustive by default**

---

## Implementation Details

### Type System

```dingo
// match must handle all cases
match value: SumType {
    Variant1(data) => ...,
    Variant2(data) => ...,
    // Compiler error if any variant is missing
}

// _ wildcard for catch-all
match value {
    SpecificCase => ...,
    _ => ...  // Handles all other cases
}
```

### Exhaustiveness Algorithm

```
1. Determine type being matched (must be sum type, enum, or interface)
2. Collect all possible variants/cases
3. For each match arm, record which variants it covers
4. Verify union of all arms equals complete set of variants
5. Generate compile error if any variant is uncovered
6. Generate warning if _ wildcard hides specific cases
```

### Pattern Grammar

```ebnf
Match      = "match" Expr "{" MatchArm+ "}"
MatchArm   = Pattern Guard? "=>" Expr ","?
Pattern    = Wildcard
           | Literal
           | Constructor "(" PatternList ")"
           | Identifier
           | Binding
Guard      = "if" Expr
Wildcard   = "_"
Binding    = identifier
Constructor = TypeName
```

### Transpilation Targets

| Pattern Type | Go Translation |
|--------------|----------------|
| Enum variant | `case EnumValue:` |
| Sum type | `if variant.tag == TagName` |
| Struct destructure | Extract fields after type check |
| Tuple destructure | Extract by index |
| Guard | Additional `if` condition |
| Wildcard `_` | `default:` case |

---

## Benefits

### Exhaustiveness Checking

```dingo
enum HttpStatus {
    Ok,
    NotFound,
    ServerError,
    Timeout
}

// ❌ Compile error: Missing Timeout case
match status {
    Ok => "success",
    NotFound => "not found",
    ServerError => "error"
}

// ✅ Compiles
match status {
    Ok => "success",
    NotFound => "not found",
    ServerError => "error",
    Timeout => "timeout"
}
```

### Type Safety

```dingo
// Dingo knows the type in each branch
match result {
    Ok(user) => {
        // user is type User here (not Option[User])
        sendEmail(user.email)
    },
    Err(error) => {
        // error is type Error here
        logError(error.message)
    }
}

// Compare to Go's verbose alternative
if result.err != nil {
    error := result.err
    logError(error.Message)
} else {
    user := *result.value
    sendEmail(user.Email)
}
```

### Expressiveness

```dingo
// Complex matching in concise syntax
status := match (isLoggedIn, hasPermission, isAdmin) {
    (false, _, _) => "Please log in",
    (true, false, false) => "No permission",
    (true, true, _) => "Access granted",
    (true, _, true) => "Admin access"
}

// Compared to nested if/else
var status string
if !isLoggedIn {
    status = "Please log in"
} else if !hasPermission && !isAdmin {
    status = "No permission"
} else if hasPermission {
    status = "Access granted"
} else if isAdmin {
    status = "Admin access"
}
```

---

## Tradeoffs

### Advantages
- ✅ **Exhaustiveness prevents bugs** (compiler enforces all cases)
- ✅ **Type-safe destructuring** (extract values safely)
- ✅ **Readable** (pattern describes structure clearly)
- ✅ **Expression-based** (can return values)

### Potential Concerns
- ❓ **New syntax** (developers must learn patterns)
  - *Mitigation:* Similar to switch, gradual adoption
- ❓ **Compilation complexity** (exhaustiveness checking is non-trivial)
  - *Mitigation:* Well-studied problem, proven algorithms
- ❓ **Debug experience** (how to step through patterns?)
  - *Mitigation:* Transpiled code maintains correspondence

---

## Implementation Complexity

**Effort:** Medium-High
**Timeline:** 2-3 weeks

### Phase 1: Parser (Week 1)
- [ ] Parse match syntax and patterns
- [ ] Support basic enum/sum type matching
- [ ] Handle wildcards and bindings
- [ ] Parser tests

### Phase 2: Type Checker (Week 1-2)
- [ ] Implement exhaustiveness checking
- [ ] Verify pattern types match scrutinee
- [ ] Handle guards and conditions
- [ ] Type checker tests

### Phase 3: Transpiler (Week 2-3)
- [ ] Generate Go switch statements
- [ ] Emit type assertions and casts
- [ ] Handle pattern destructuring
- [ ] Integration tests

### Phase 4: Advanced Patterns (Future)
- [ ] Struct destructuring
- [ ] Nested patterns
- [ ] Array/slice patterns
- [ ] Range patterns
- [ ] Where guards with nested patterns (Priority: Low)
  - Currently: `where` guards only support simple patterns
  - Future: Support `Result_Ok(Option_Some(val)) where val > 0`
  - Complexity: ~150 lines, 4.5 hours
  - Workaround: Use nested match expressions
  - Note: Simple patterns cover 95%+ of real-world use cases

---

## Examples

### Example 1: HTTP Response Handling

```dingo
enum HttpResponse {
    Ok(body: string),
    NotFound,
    ServerError(code: int, message: string),
    Redirect(url: string)
}

func handleResponse(resp: HttpResponse) -> string {
    match resp {
        Ok(body) => "Success: ${body}",
        NotFound => "404: Not found",
        ServerError(code, msg) => "Error ${code}: ${msg}",
        Redirect(url) => "Redirecting to ${url}"
    }
}
```

### Example 2: AST Processing

```dingo
enum Expr {
    Number(value: int),
    Add(left: Expr, right: Expr),
    Multiply(left: Expr, right: Expr)
}

func eval(expr: Expr) -> int {
    match expr {
        Number(n) => n,
        Add(left, right) => eval(left) + eval(right),
        Multiply(left, right) => eval(left) * eval(right)
    }
}
```

### Example 3: State Machine

```dingo
enum State {
    Idle,
    Loading(progress: float),
    Success(data: string),
    Error(error: string)
}

func render(state: State) -> string {
    match state {
        Idle => renderIdle(),
        Loading(progress) if progress < 50.0 => "Starting...",
        Loading(progress) => "Loading: ${progress}%",
        Success(data) => renderSuccess(data),
        Error(err) => renderError(err)
    }
}
```

---

## Success Criteria

- [ ] Match expressions support all sum types and enums
- [ ] Exhaustiveness checking catches missing cases at compile-time
- [ ] Pattern destructuring extracts values correctly
- [ ] Guards enable conditional matching
- [ ] Transpiled code is efficient (no performance overhead)
- [ ] IDE provides autocomplete for missing patterns
- [ ] Positive feedback from Rust/Kotlin developers

---

## References

- Rust Pattern Matching: https://doc.rust-lang.org/book/ch18-00-patterns.html
- Swift Pattern Matching: https://docs.swift.org/swift-book/documentation/the-swift-programming-language/patterns/
- Kotlin When Expression: https://kotlinlang.org/docs/control-flow.html#when-expression
- OCaml Pattern Matching (theoretical foundation)

---

## Next Steps

1. Design pattern syntax and grammar
2. Implement exhaustiveness checking algorithm
3. Prototype transpilation for basic enums
4. Test with Result and Option types
5. Measure code quality improvement vs Go switch
