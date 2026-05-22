package ast

import (
	"os"
	"path/filepath"
	"strings"
)

// ExtractEnumRegistryFromDir walks the directory containing currentFile and
// builds a single EnumRegistry that includes enum declarations from every
// sibling .dingo file (excluding currentFile itself).
//
// Why: ExtractFullEnumRegistry only sees enums declared in the source it's
// handed. That works inside a single file, but a `match e { Variant => ... }`
// expression in file A.dingo cannot resolve to the variant when its enum is
// declared in B.dingo — the registry for A doesn't know B exists.
//
// This function is a per-directory pass that scans sibling files at the same
// nesting level as currentFile and unions their enum declarations into one
// registry. The result is intended to be Merged into the per-file registry
// built from the file under transpilation.
//
// Conservative behaviour: any sibling that fails to parse is silently skipped
// — we never want a malformed file to fail an otherwise-valid build. If the
// directory cannot be read at all, returns nil.
func ExtractEnumRegistryFromDir(currentFile string) *EnumRegistry {
	dir := filepath.Dir(currentFile)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	currentBase := filepath.Base(currentFile)
	combined := NewEnumRegistry()
	found := false

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == currentBase {
			continue // skip self; the caller already has the local registry
		}
		if !strings.HasSuffix(name, ".dingo") {
			continue
		}
		sib, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		sibReg := ExtractFullEnumRegistry(sib)
		if sibReg == nil {
			continue
		}
		combined.Merge(sibReg)
		found = true
	}

	if !found {
		return nil
	}
	return combined
}

// ExtractEnumRegistryFromImports parses the Go imports in src and scans each
// imported directory for enum declarations. Returns a merged registry, or
// nil if no relevant enums were found.
//
// Resolves import paths against the module root inferred from currentFile:
// walks up the directory tree from currentFile looking for either a go.mod
// or a `src/` ancestor (the GOROOT pattern). Once the root is found, each
// import path is joined onto root/src to produce a candidate directory.
// Imports that don't resolve to an actual directory are silently skipped.
//
// This complements ExtractEnumRegistryFromDir: that pass picks up sibling
// files in the SAME directory, this pass picks up cross-package enums
// (e.g. a `match e *ir.E_ { ... }` in cmd/compile/internal/walk needs the
// enum declared in cmd/compile/internal/ir).
//
// Conservative: any failure (missing module root, unreadable directory,
// parse error inside a sibling) is silently skipped — we never want to
// fail a build because of a cross-package enum scan.
func ExtractEnumRegistryFromImports(currentFile string, src []byte) *EnumRegistry {
	srcDir := findModuleSrcRoot(currentFile)
	if srcDir == "" {
		return nil
	}

	imports := extractImportPaths(src)
	if len(imports) == 0 {
		return nil
	}

	combined := NewEnumRegistry()
	found := false
	for _, imp := range imports {
		dir := filepath.Join(srcDir, imp)
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			continue
		}
		// Package alias: last path segment of the import. This is the
		// default Go convention — `import "cmd/compile/internal/ir"`
		// brings in identifiers under `ir.`. Aliased imports
		// (`foo "cmd/.../ir"`) are not handled here; their types would
		// be referenced as `foo.E_TagX`, but the import-paths scanner
		// drops the alias.
		pkgAlias := filepath.Base(imp)
		// Scan only top-level .dingo files in the imported directory —
		// nested packages have their own enum decls and need their own
		// scan, but a single-package enum lives at the top level.
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".dingo") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
			if err != nil {
				continue
			}
			impReg := ExtractFullEnumRegistry(data)
			if impReg == nil {
				continue
			}
			// Tag every enum picked up from this directory with the
			// import's package alias so cross-package match codegen can
			// qualify <Enum>Tag<Variant> as <pkg>.<Enum>Tag<Variant>.
			for enumName := range impReg.SharedFieldsEnums {
				impReg.EnumPackage[enumName] = pkgAlias
			}
			for _, enumName := range impReg.SumTypeVariants {
				if _, ok := impReg.EnumPackage[enumName]; !ok {
					impReg.EnumPackage[enumName] = pkgAlias
				}
			}
			combined.Merge(impReg)
			found = true
		}
	}
	if !found {
		return nil
	}
	return combined
}

// findModuleSrcRoot walks up from filename looking for a `src/` directory
// (the GOROOT layout where all packages live under src/<pkg>/) or a go.mod
// marker. Returns the path under which import paths should be resolved
// (i.e. `<root>/src` for GOROOT, or the go.mod dir for modules). Returns
// "" if no recognised root is found.
//
// `src/` wins over `go.mod` when both appear on the walk: the Go compiler
// tree has nested go.mod files (e.g. cmd/go.mod) that would otherwise
// trap the search before reaching the outer GOROOT/src/. Pre-scanning
// for a `src/` ancestor first matches the convention that import paths
// like "cmd/compile/internal/ir" resolve relative to the outermost
// GOROOT/src directory.
func findModuleSrcRoot(filename string) string {
	// First pass: look for `src/` ancestor (preferred).
	dir := filepath.Dir(filename)
	for {
		if base := filepath.Base(dir); base == "src" {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	// Second pass: nearest go.mod.
	dir = filepath.Dir(filename)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// extractImportPaths scans Go source for top-level `import` declarations
// and returns the package paths (without quotes). Handles both the
// single-line `import "x"` form and the grouped `import ( "x"; "y" )`
// form.
func extractImportPaths(src []byte) []string {
	s := string(src)
	var paths []string
	i := 0
	for {
		idx := strings.Index(s[i:], "import")
		if idx < 0 {
			break
		}
		j := i + idx + len("import")
		// Token boundary check on both sides — avoid matching e.g. `importer`.
		if i+idx > 0 {
			prev := s[i+idx-1]
			if isIdentByte(prev) {
				i = j
				continue
			}
		}
		if j < len(s) && isIdentByte(s[j]) {
			i = j
			continue
		}
		// Skip whitespace.
		for j < len(s) && (s[j] == ' ' || s[j] == '\t' || s[j] == '\n') {
			j++
		}
		if j >= len(s) {
			break
		}
		if s[j] == '"' {
			// Single import.
			end := strings.IndexByte(s[j+1:], '"')
			if end < 0 {
				break
			}
			paths = append(paths, s[j+1:j+1+end])
			i = j + 1 + end + 1
			continue
		}
		if s[j] == '(' {
			// Grouped import. Read until the matching ')'.
			j++
			closeIdx := strings.IndexByte(s[j:], ')')
			if closeIdx < 0 {
				break
			}
			block := s[j : j+closeIdx]
			i = j + closeIdx + 1
			// Pull every quoted string out of the block. Each quoted
			// string is one import path; the optional alias before it is
			// ignored.
			k := 0
			for k < len(block) {
				q := strings.IndexByte(block[k:], '"')
				if q < 0 {
					break
				}
				start := k + q + 1
				end := strings.IndexByte(block[start:], '"')
				if end < 0 {
					break
				}
				paths = append(paths, block[start:start+end])
				k = start + end + 1
			}
			continue
		}
		// Unrecognised form — bail out, this import was malformed.
		i = j
	}
	return paths
}

func isIdentByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_'
}

// Merge adds every enum declaration recorded in other into r. Collisions are
// not re-checked here — same-named variants across files are treated as
// equivalent registrations (the registry already deduplicates).
//
// Safe on a nil receiver/argument: both no-op cases return without mutation.
func (r *EnumRegistry) Merge(other *EnumRegistry) {
	if r == nil || other == nil {
		return
	}
	for variant, enumName := range other.SumTypeVariants {
		if _, exists := r.SumTypeVariants[variant]; !exists {
			r.SumTypeVariants[variant] = enumName
			r.EnumToVariants[enumName] = append(r.EnumToVariants[enumName], variant)
		}
	}
	for variant := range other.PointerVariants {
		r.PointerVariants[variant] = true
	}
	for enumName := range other.SharedFieldsEnums {
		r.SharedFieldsEnums[enumName] = true
	}
	for variant, info := range other.ValueEnumVariants {
		if _, exists := r.ValueEnumVariants[variant]; !exists {
			r.ValueEnumVariants[variant] = info
		}
	}
	if r.EnumPackage == nil {
		r.EnumPackage = make(map[string]string)
	}
	for enumName, pkg := range other.EnumPackage {
		// Don't overwrite a local registration with an import-scan
		// entry: an enum declared locally trumps a same-named foreign
		// enum (the latter wouldn't compile anyway).
		if _, ok := r.EnumPackage[enumName]; !ok {
			r.EnumPackage[enumName] = pkg
		}
	}
}
