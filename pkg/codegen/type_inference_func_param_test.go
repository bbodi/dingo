package codegen

import (
	"strings"
	"testing"
)

// TestFindEnclosingFunctionIgnoresFuncTypeParams verifies that
// findEnclosingFunctionFallback (the scanner-based path used when go/parser
// can't parse Dingo-extended syntax) does not treat a function-TYPE parameter
// or local function-typed variable as the enclosing function for code
// appearing later.
//
// Regression: the fallback used to push every `func` token onto its
// candidate list, so a parameter `lookup func(path string) error` was
// indistinguishable from a function literal with a body. Code later in the
// enclosing function would then resolve to a "(closure)" with no return
// types, breaking `?` propagation with a misleading error message.
func TestFindEnclosingFunctionIgnoresFuncTypeParams(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		exprMark string // a unique substring; exprPos is positioned right before it
		wantName string // expected enclosing function name
	}{
		{
			name: "func-type parameter doesn't masquerade as closure",
			src: `package main
import "io"
func Import(lookup func(p string) (io.ReadCloser, error)) (pkg int, err error) {
	if lookup != nil {
		x := lookup("test")
		_ = x
	}
	return 0, nil
}
`,
			exprMark: `x := lookup`,
			wantName: "Import",
		},
		{
			name: "func-type field on struct doesn't masquerade as closure",
			src: `package main
type S struct {
	cb func(int) error
}
func (s *S) Run() error {
	x := s.cb(1)
	_ = x
	return nil
}
`,
			exprMark: `x := s.cb`,
			wantName: "Run",
		},
		{
			name: "func-type return doesn't masquerade as closure",
			src: `package main
func factory() func(int) error {
	return nil
}
func use() error {
	x := factory()
	_ = x
	return nil
}
`,
			exprMark: `x := factory`,
			wantName: "use",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pos := strings.Index(tt.src, tt.exprMark)
			if pos < 0 {
				t.Fatalf("could not find marker %q in source", tt.exprMark)
			}
			fd := findEnclosingFunctionFallback([]byte(tt.src), pos)
			if fd == nil {
				t.Fatalf("findEnclosingFunctionFallback returned nil; want enclosing %q", tt.wantName)
			}
			name := ""
			if fd.Name != nil {
				name = fd.Name.Name
			}
			if name != tt.wantName {
				t.Errorf("enclosing function name = %q, want %q", name, tt.wantName)
			}
		})
	}
}
