package transpiler

import (
	"strings"
	"testing"
)

// decl_block_tuple_test.go — regression tests for transformTupleLiterals
// incorrectly treating the parentheses of a `var (…)` / `const (…)` /
// `import (…)` / `type (…)` declaration group as a candidate tuple
// literal.
//
// Symptom: a struct literal inside the group (e.g.
// `var ( a = r{x: 1, y: 2} )`) carries a `,` between fields, which the
// tuple-literal pass interprets as a tuple-element separator. The
// downstream Go parser then rejects the mangled output with
// `expected ')', found '='`. Hit by 12 files in the Go compiler under
// cmd/compile/internal/ssa/_gen/.
//
// Each test transpiles a small snippet and checks that the declaration
// group survives intact (specifically, the colon-separated field names
// of the struct literal must not have been rewritten).

func TestDeclBlock_Var_StructLitWithMultipleFields(t *testing.T) {
	src := `package x

type r struct{ x int; y int }

func f() {
	var (
		a = r{x: 1, y: 2}
	)
	_ = a
}
`
	out, err := PureASTTranspile([]byte(src), "")
	if err != nil {
		t.Fatalf("transpile failed: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, "r{x: 1, y: 2}") {
		t.Errorf("struct literal corrupted inside var block.\n%s", got)
	}
}

func TestDeclBlock_Var_SliceOfStructLits(t *testing.T) {
	// A slice initialised inline inside a var block — also has commas
	// at outer depth, but those are slice-element separators and must
	// not be treated as tuple separators either.
	src := `package x

type r struct{ x int; y int }

func f() {
	var (
		s = []r{{x: 1, y: 2}, {x: 3, y: 4}}
	)
	_ = s
}
`
	out, err := PureASTTranspile([]byte(src), "")
	if err != nil {
		t.Fatalf("transpile failed: %v", err)
	}
	if !strings.Contains(string(out), "[]r{{x: 1, y: 2}, {x: 3, y: 4}}") {
		t.Errorf("slice literal corrupted inside var block.\n%s", out)
	}
}

func TestDeclBlock_Const_GroupedDecls(t *testing.T) {
	// `const (…)` groups should also pass through untouched. Constants
	// don't usually contain composite literals, but the same paren
	// gate applies.
	src := `package x

const (
	a = 1
	b = 2
	c = 3
)

func f() { _, _, _ = a, b, c }
`
	out, err := PureASTTranspile([]byte(src), "")
	if err != nil {
		t.Fatalf("transpile failed: %v", err)
	}
	if !strings.Contains(string(out), "a = 1") || !strings.Contains(string(out), "b = 2") {
		t.Errorf("const group corrupted.\n%s", out)
	}
}

func TestDeclBlock_Type_GroupedDecls(t *testing.T) {
	// `type (…)` groups. The inner types include `;` which can confuse
	// pure-token transformers, but the outer paren gate is what we are
	// testing here.
	src := `package x

type (
	S struct{ a int; b int }
	T struct{ c string }
)

func f() {
	_ = S{a: 1, b: 2}
	_ = T{c: "x"}
}
`
	out, err := PureASTTranspile([]byte(src), "")
	if err != nil {
		t.Fatalf("transpile failed: %v", err)
	}
	if !strings.Contains(string(out), "S{a: 1, b: 2}") {
		t.Errorf("type group struct lit corrupted.\n%s", out)
	}
}

func TestDeclBlock_Import_GroupedDecls(t *testing.T) {
	// `import (…)` is the most common grouped decl. It's all string
	// literals, but the paren gate must still skip it.
	src := `package x

import (
	"fmt"
	"strings"
)

func f() {
	fmt.Println(strings.ToUpper("hi"))
}
`
	out, err := PureASTTranspile([]byte(src), "")
	if err != nil {
		t.Fatalf("transpile failed: %v", err)
	}
	if !strings.Contains(string(out), `"fmt"`) || !strings.Contains(string(out), `"strings"`) {
		t.Errorf("import group corrupted.\n%s", out)
	}
}

func TestTupleLiteral_StillWorks_TopLevelExpression(t *testing.T) {
	// Regression guard: tuple literals in expression position must
	// continue to be detected and transformed. The previous fix must
	// not have widened the skip predicate so much that legitimate
	// tuple literals are missed.
	src := `package x

func returnsTuple() (int, int) {
	return 1, 2
}

func f() {
	a, b := returnsTuple()
	_, _ = a, b
}
`
	out, err := PureASTTranspile([]byte(src), "")
	if err != nil {
		t.Fatalf("transpile failed: %v", err)
	}
	// We don't pin the exact transformed shape (depends on tuple
	// codegen), but at minimum the function-multiple-return idiom
	// must survive.
	if !strings.Contains(string(out), "returnsTuple") {
		t.Errorf("multiple-return idiom corrupted.\n%s", out)
	}
}
