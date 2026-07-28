// Package staticgate holds test helpers for the P0-TEST-001 static
// invariants gate — checks that are about the shape of the codebase
// itself (import layering), not the behavior of any one package.
package staticgate

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// modulePrefix is this module's own import-path root; every internal
// package name checked below is built from it.
const modulePrefix = "github.com/VENOMDRMSUPPORT/venom-router/internal/"

// goListPackage mirrors the two fields of `go list -json` output this
// suite needs: the package's own import path, and Deps — the full,
// already-transitive set of import paths it depends on (`go list`
// computes this itself; no recursive walk is needed here).
type goListPackage struct {
	ImportPath string
	Deps       []string
}

// listInternalPackages runs `go list -json ./internal/...` from the
// repo root and returns every internal package keyed by import path.
//
// Acyclicity is not separately re-implemented here: Go's own compiler
// refuses to build any package graph containing an import cycle, so a
// successful `go list` (which this helper requires to succeed) is itself
// proof the internal/* graph is acyclic. What this suite adds on top is
// the repo's specific FORBIDDEN-EDGE rules (08 §2 / 01 §3), which the
// compiler has no opinion on — those are asserted explicitly below.
func listInternalPackages(t *testing.T) map[string]goListPackage {
	t.Helper()

	repoRoot := repoRootFromThisFile(t)

	out, err := runGoList(repoRoot)
	if err != nil {
		t.Fatalf("go list -json ./internal/...: %v", err)
	}

	pkgs := make(map[string]goListPackage)
	dec := json.NewDecoder(strings.NewReader(out))
	for dec.More() {
		var p goListPackage
		if err := dec.Decode(&p); err != nil {
			t.Fatalf("decode go list output: %v", err)
		}
		pkgs[p.ImportPath] = p
	}
	return pkgs
}

func repoRootFromThisFile(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	// This file lives at <repoRoot>/internal/staticgate/layering_test.go.
	return filepath.Join(filepath.Dir(thisFile), "..", "..")
}

// runGoList runs `go list -json ./internal/...` in repoRoot and returns
// its stdout. `go list -json` already populates each package's "Deps"
// field with its full transitive dependency import paths, so no
// separate recursive walk is needed.
func runGoList(repoRoot string) (string, error) {
	cmd := exec.Command("go", "list", "-json", "./internal/...")
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("%w: %s", err, exitErr.Stderr)
		}
		return "", err
	}
	return string(out), nil
}

// hasForbiddenDep reports which of pkg's dependencies match one of
// forbiddenPrefixes, either exactly or as a sub-package (prefix + "/").
func hasForbiddenDep(pkg goListPackage, forbiddenPrefixes ...string) []string {
	var hits []string
	for _, d := range pkg.Deps {
		for _, prefix := range forbiddenPrefixes {
			if d == prefix || strings.HasPrefix(d, prefix+"/") {
				hits = append(hits, d)
			}
		}
	}
	return hits
}

// TestLayering_DomainPackagesImportNoInfrastructure asserts 08 §2 / 01
// §3: the pure domain packages (accounts/domain, providers, models)
// import no storage, database/sql, or HTTP/infrastructure package.
func TestLayering_DomainPackagesImportNoInfrastructure(t *testing.T) {
	pkgs := listInternalPackages(t)

	domainPackages := []string{
		modulePrefix + "accounts/domain",
		modulePrefix + "providers",
		modulePrefix + "models",
		modulePrefix + "intelligence",
		modulePrefix + "quota",
		modulePrefix + "routing",
	}
	forbidden := []string{
		modulePrefix + "storage",
		modulePrefix + "httpapi",
		modulePrefix + "httpui",
		"database/sql",
		"net/http",
	}

	for _, name := range domainPackages {
		pkg, ok := pkgs[name]
		if !ok {
			t.Fatalf("expected package %q not found via go list — has it moved or been removed?", name)
		}
		if hits := hasForbiddenDep(pkg, forbidden...); len(hits) > 0 {
			t.Errorf("layering violation: %q imports forbidden infrastructure: %v", name, hits)
		}
	}
}

// TestLayering_ProvidersImportsNeitherAccountsNorModels asserts 01 §3:
// providers imports neither accounts/* nor models.
func TestLayering_ProvidersImportsNeitherAccountsNorModels(t *testing.T) {
	pkgs := listInternalPackages(t)

	const providers = modulePrefix + "providers"
	pkg, ok := pkgs[providers]
	if !ok {
		t.Fatalf("expected package %q not found via go list", providers)
	}

	forbidden := []string{
		modulePrefix + "accounts",
		modulePrefix + "models",
	}
	if hits := hasForbiddenDep(pkg, forbidden...); len(hits) > 0 {
		t.Errorf("layering violation: %q imports forbidden package(s): %v", providers, hits)
	}
}

// TestLayering_StorageDependencyIsOneDirectionOnly asserts 01 §3:
// storage may import domain packages, never the reverse — i.e. none of
// the domain packages may import storage. (storage importing domain is
// permitted and is not checked here — only that direction is fine.)
func TestLayering_StorageDependencyIsOneDirectionOnly(t *testing.T) {
	pkgs := listInternalPackages(t)

	domainPackages := []string{
		modulePrefix + "accounts/domain",
		modulePrefix + "accounts/application",
		modulePrefix + "providers",
		modulePrefix + "models",
		modulePrefix + "intelligence",
		modulePrefix + "quota",
		modulePrefix + "routing",
	}
	const storage = modulePrefix + "storage"

	for _, name := range domainPackages {
		pkg, ok := pkgs[name]
		if !ok {
			t.Fatalf("expected package %q not found via go list", name)
		}
		if hits := hasForbiddenDep(pkg, storage); len(hits) > 0 {
			t.Errorf("layering violation: %q imports %q (storage may depend on domain, never the reverse)", name, hits)
		}
	}
}
