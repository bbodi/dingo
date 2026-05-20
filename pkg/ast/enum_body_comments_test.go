package ast

import (
	"strings"
	"testing"
)

// enum_body_comments_test.go — regression tests for `// ...` and
// `/* ... */` comments inside an enum body.
//
// History: the enum parser's whitespace skippers consumed only
// spaces/tabs/newlines/commas. A comment between variants left the
// cursor at `/`, which is not alpha, so parseIdent failed with
// "expected identifier"; ParseEnumDecl propagated the error and
// TransformEnumSource fell back to copying `enum` verbatim, after
// which Go's parser rejected the surrounding source with
// `expected declaration, found enum`. Hit while writing the
// `expr_enum.dingo` parallel enum for the Go compiler's ir package
// — natural to want a `// AddrExpr — &X` doc per variant.

func TestEnumParser_LineCommentBetweenVariants(t *testing.T) {
	src := `enum E {
    *Foo {}
    // a comment between variants
    *Bar {}
}`
	p := NewEnumParser([]byte(src), 0)
	decl, _, err := p.ParseEnumDecl()
	if err != nil {
		t.Fatalf("ParseEnumDecl: %v", err)
	}
	if got, want := len(decl.Variants), 2; got != want {
		t.Fatalf("variants = %d, want %d", got, want)
	}
	if got := decl.Variants[0].Name.Name; got != "Foo" {
		t.Errorf("variants[0] = %q, want Foo", got)
	}
	if got := decl.Variants[1].Name.Name; got != "Bar" {
		t.Errorf("variants[1] = %q, want Bar", got)
	}
}

func TestEnumParser_LineCommentBeforeFirstVariant(t *testing.T) {
	// A `//` comment as the very first body token (before the first
	// variant) must also be tolerated.
	src := `enum E {
    // header comment inside the body
    *Foo {}
    *Bar {}
}`
	p := NewEnumParser([]byte(src), 0)
	decl, _, err := p.ParseEnumDecl()
	if err != nil {
		t.Fatalf("ParseEnumDecl: %v", err)
	}
	if got, want := len(decl.Variants), 2; got != want {
		t.Fatalf("variants = %d, want %d", got, want)
	}
}

func TestEnumParser_LineCommentAfterLastVariant(t *testing.T) {
	// A `//` comment between the last variant and the closing brace.
	src := `enum E {
    *Foo {}
    *Bar {}
    // trailing comment
}`
	p := NewEnumParser([]byte(src), 0)
	decl, _, err := p.ParseEnumDecl()
	if err != nil {
		t.Fatalf("ParseEnumDecl: %v", err)
	}
	if got, want := len(decl.Variants), 2; got != want {
		t.Fatalf("variants = %d, want %d", got, want)
	}
}

func TestEnumParser_BlockCommentBetweenVariants(t *testing.T) {
	// /* ... */ block comments, single-line.
	src := `enum E {
    *Foo {}
    /* explanatory note */
    *Bar {}
}`
	p := NewEnumParser([]byte(src), 0)
	decl, _, err := p.ParseEnumDecl()
	if err != nil {
		t.Fatalf("ParseEnumDecl: %v", err)
	}
	if got, want := len(decl.Variants), 2; got != want {
		t.Fatalf("variants = %d, want %d", got, want)
	}
}

func TestEnumParser_BlockCommentMultiline(t *testing.T) {
	// Multi-line /* ... */ block comments.
	src := `enum E {
    /*
     * Multi-line block comment
     * spanning several lines
     */
    *Foo {}
    *Bar {}
}`
	p := NewEnumParser([]byte(src), 0)
	decl, _, err := p.ParseEnumDecl()
	if err != nil {
		t.Fatalf("ParseEnumDecl: %v", err)
	}
	if got, want := len(decl.Variants), 2; got != want {
		t.Fatalf("variants = %d, want %d", got, want)
	}
}

func TestEnumParser_CommentInsideSharedBlock(t *testing.T) {
	// `// ...` comments must also be tolerated inside the shared
	// block (between field declarations).
	src := `enum E {
    shared {
        pos: int,
        // explanation of op
        op: int,
    }
    *Foo {}
}`
	p := NewEnumParser([]byte(src), 0)
	decl, _, err := p.ParseEnumDecl()
	if err != nil {
		t.Fatalf("ParseEnumDecl: %v", err)
	}
	if !decl.HasSharedFields() {
		t.Errorf("expected shared block to parse")
	}
	if got, want := len(decl.SharedFields), 2; got != want {
		t.Errorf("shared fields = %d, want %d", got, want)
	}
}

func TestEnumParser_CommentInsideStructVariant(t *testing.T) {
	// `// ...` comment between field declarations in a struct variant.
	src := `enum E {
    *Foo {
        x: int,
        // documenting y
        y: int,
    }
}`
	p := NewEnumParser([]byte(src), 0)
	decl, _, err := p.ParseEnumDecl()
	if err != nil {
		t.Fatalf("ParseEnumDecl: %v", err)
	}
	if got, want := len(decl.Variants[0].Fields), 2; got != want {
		t.Errorf("fields = %d, want %d", got, want)
	}
}

func TestEnumParser_MultipleCommentsBetweenVariants(t *testing.T) {
	// A run of comments + blank lines, the natural shape of dense doc.
	src := `enum E {
    *Foo {}

    // one line
    // another line
    // a third line
    *Bar {}
}`
	p := NewEnumParser([]byte(src), 0)
	decl, _, err := p.ParseEnumDecl()
	if err != nil {
		t.Fatalf("ParseEnumDecl: %v", err)
	}
	if got, want := len(decl.Variants), 2; got != want {
		t.Fatalf("variants = %d, want %d", got, want)
	}
}

func TestEnumCodeGen_CommentsDoNotAppearInOutput(t *testing.T) {
	// Sanity: enum codegen should not emit the dingo comments
	// verbatim into the Go source. The codegen doesn't see them at all
	// because they're discarded during parsing — pin this so a future
	// "preserve comments" refactor doesn't accidentally leak them into
	// the wrong positions.
	src := `enum E {
    // this comment should be discarded
    *Foo {}
    /* this one too */
    *Bar {}
}`
	p := NewEnumParser([]byte(src), 0)
	decl, _, err := p.ParseEnumDecl()
	if err != nil {
		t.Fatalf("ParseEnumDecl: %v", err)
	}
	got := string(NewEnumCodeGen().Generate(decl, "", 0, 0))
	if strings.Contains(got, "should be discarded") {
		t.Errorf("line comment leaked into generated Go.\n%s", got)
	}
	if strings.Contains(got, "this one too") {
		t.Errorf("block comment leaked into generated Go.\n%s", got)
	}
}
