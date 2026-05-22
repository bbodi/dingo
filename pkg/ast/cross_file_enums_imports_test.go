package ast

import (
	"os"
	"path/filepath"
	"testing"
)

// TestExtractEnumRegistryFromImports_GorootLayout verifies that enum
// declarations in imported packages are picked up when the source tree
// follows the GOROOT/src/<pkg> layout.
func TestExtractEnumRegistryFromImports_GorootLayout(t *testing.T) {
	root := t.TempDir()
	// Set up a GOROOT-shaped tree:
	//   root/src/cmd/compile/internal/ir/expr_enum.dingo  (declares E_)
	//   root/src/cmd/compile/internal/walk/assign.dingo   (importing ir)
	irDir := filepath.Join(root, "src", "cmd", "compile", "internal", "ir")
	walkDir := filepath.Join(root, "src", "cmd", "compile", "internal", "walk")
	if err := os.MkdirAll(irDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(walkDir, 0755); err != nil {
		t.Fatal(err)
	}

	enumFile := filepath.Join(irDir, "expr_enum.dingo")
	if err := os.WriteFile(enumFile, []byte(`enum E_ {
    shared { pos: int }
    *IndexExpr { X: int, Index: int }
    *ParenExpr { X: int }
}
`), 0644); err != nil {
		t.Fatal(err)
	}

	walkFile := filepath.Join(walkDir, "assign.dingo")
	walkSrc := []byte(`package walk

import (
	"cmd/compile/internal/ir"
)

func walk(e *ir.E_) {}
`)
	if err := os.WriteFile(walkFile, walkSrc, 0644); err != nil {
		t.Fatal(err)
	}

	reg := ExtractEnumRegistryFromImports(walkFile, walkSrc)
	if reg == nil {
		t.Fatalf("ExtractEnumRegistryFromImports returned nil; expected imports to resolve")
	}
	if !reg.IsSharedFieldsEnum("E_") {
		t.Errorf("E_ should be marked as shared-fields enum (imported from ir/)")
	}
	if _, ok := reg.IsSumTypeVariant("IndexExpr"); !ok {
		t.Errorf("IndexExpr variant should be registered from imported ir/")
	}
	if _, ok := reg.IsSumTypeVariant("ParenExpr"); !ok {
		t.Errorf("ParenExpr variant should be registered from imported ir/")
	}
}

// TestExtractEnumRegistryFromImports_ModuleLayout uses a go.mod-rooted tree
// instead of a src/-rooted one.
func TestExtractEnumRegistryFromImports_ModuleLayout(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/m\n"), 0644); err != nil {
		t.Fatal(err)
	}
	pkgA := filepath.Join(root, "pkga")
	pkgB := filepath.Join(root, "pkgb")
	if err := os.MkdirAll(pkgA, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(pkgB, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgA, "e.dingo"), []byte(`enum T {
    *Foo {}
    *Bar { X: int }
}
`), 0644); err != nil {
		t.Fatal(err)
	}
	consumerFile := filepath.Join(pkgB, "c.dingo")
	consumerSrc := []byte(`package pkgb

import "pkga"
`)
	if err := os.WriteFile(consumerFile, consumerSrc, 0644); err != nil {
		t.Fatal(err)
	}

	reg := ExtractEnumRegistryFromImports(consumerFile, consumerSrc)
	if reg == nil {
		t.Fatalf("ExtractEnumRegistryFromImports returned nil; expected pkga's T to be picked up")
	}
	if _, ok := reg.IsSumTypeVariant("Foo"); !ok {
		t.Errorf("Foo variant should be registered from imported pkga")
	}
	if _, ok := reg.IsSumTypeVariant("Bar"); !ok {
		t.Errorf("Bar variant should be registered from imported pkga")
	}
}

// TestExtractEnumRegistryFromImports_GorootBeatsNestedGoMod is a guard
// regression: the Go compiler tree has a nested go.mod at
// GOROOT/src/cmd/go.mod, but the import paths in cmd/compile/internal/walk
// are still relative to GOROOT/src, NOT to GOROOT/src/cmd. The detector
// must prefer the `src/` ancestor over the nested go.mod, otherwise
// `import "cmd/compile/internal/ir"` would resolve to
// `<src>/cmd/cmd/compile/internal/ir` (wrong) and the enum scan misses
// the import entirely.
func TestExtractEnumRegistryFromImports_GorootBeatsNestedGoMod(t *testing.T) {
	root := t.TempDir()
	// Mirror GOROOT layout with a nested go.mod in src/cmd/, the way
	// /home/sharp/dev/misc/go-src/src/cmd/go.mod sits in the real tree.
	if err := os.MkdirAll(filepath.Join(root, "src", "cmd"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "cmd", "go.mod"),
		[]byte("module cmd\n"), 0644); err != nil {
		t.Fatal(err)
	}

	irDir := filepath.Join(root, "src", "cmd", "compile", "internal", "ir")
	walkDir := filepath.Join(root, "src", "cmd", "compile", "internal", "walk")
	if err := os.MkdirAll(irDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(walkDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(irDir, "e.dingo"), []byte(`enum E_ {
    shared { pos: int }
    *NilExpr {}
}
`), 0644); err != nil {
		t.Fatal(err)
	}
	walkFile := filepath.Join(walkDir, "w.dingo")
	walkSrc := []byte(`package walk

import "cmd/compile/internal/ir"
`)
	if err := os.WriteFile(walkFile, walkSrc, 0644); err != nil {
		t.Fatal(err)
	}

	reg := ExtractEnumRegistryFromImports(walkFile, walkSrc)
	if reg == nil {
		t.Fatalf("registry nil: detector stopped at nested go.mod instead of finding `src/` root")
	}
	if _, ok := reg.IsSumTypeVariant("NilExpr"); !ok {
		t.Errorf("NilExpr should be registered from imported ir/")
	}
}

// TestExtractEnumRegistryFromImports_IgnoresMissingPackages verifies that
// an import that doesn't resolve to a real directory (e.g. a stdlib
// import) is silently skipped rather than failing.
func TestExtractEnumRegistryFromImports_IgnoresMissingPackages(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/m\n"), 0644); err != nil {
		t.Fatal(err)
	}
	pkgA := filepath.Join(root, "pkga")
	if err := os.MkdirAll(pkgA, 0755); err != nil {
		t.Fatal(err)
	}
	consumerFile := filepath.Join(pkgA, "c.dingo")
	consumerSrc := []byte(`package pkga

import (
	"fmt"
	"some/missing/pkg"
	"os"
)
`)
	if err := os.WriteFile(consumerFile, consumerSrc, 0644); err != nil {
		t.Fatal(err)
	}

	// Should not panic, should not error — just returns nil because
	// nothing useful was found.
	_ = ExtractEnumRegistryFromImports(consumerFile, consumerSrc)
}
