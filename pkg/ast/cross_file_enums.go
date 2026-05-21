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
}
