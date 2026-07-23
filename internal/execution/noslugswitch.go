package execution

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
)

// SlugSwitchViolation describes one switch-on-string-literal-cases
// finding — the syntactic shape 01 §4.5 forbids ("never a switch on
// provider slug string"). Transport selection must instead go through a
// typed capability lookup (TransportType via TransportTypeResolver; see
// dispatcher.go).
type SlugSwitchViolation struct {
	File string
	Line int
}

func (v SlugSwitchViolation) String() string {
	return fmt.Sprintf("%s:%d: switch on string-literal cases is forbidden (01 §4.5) — select by typed capability instead", v.File, v.Line)
}

// CheckNoSlugSwitch parses every non-test .go file directly within dir
// (no recursion into subpackages) and reports every value switch
// statement whose case clauses are all plain string literals. That is
// the syntactic shape of "switch on a provider slug string"; a type
// switch (switch x.(type)) is a different construct and is never
// flagged.
//
// Full CI wiring of this check into the repo-wide static-gate suite is
// P0-TEST-001's job; this function and its own test are this unit's
// enforcement mechanism for internal/execution specifically.
func CheckNoSlugSwitch(dir string) ([]SlugSwitchViolation, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("execution: read dir %q: %w", dir, err)
	}

	var violations []SlugSwitchViolation
	fset := token.NewFileSet()

	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".go" {
			continue
		}

		path := filepath.Join(dir, e.Name())
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return nil, fmt.Errorf("execution: parse %q: %w", path, err)
		}

		ast.Inspect(file, func(n ast.Node) bool {
			sw, ok := n.(*ast.SwitchStmt)
			if !ok || sw.Tag == nil || !allCasesAreStringLiterals(sw) {
				return true
			}
			pos := fset.Position(sw.Pos())
			violations = append(violations, SlugSwitchViolation{File: pos.Filename, Line: pos.Line})
			return true
		})
	}

	return violations, nil
}

// allCasesAreStringLiterals reports whether sw has at least one
// non-default case clause and every case value in every non-default
// clause is a plain string literal.
func allCasesAreStringLiterals(sw *ast.SwitchStmt) bool {
	found := false
	for _, stmt := range sw.Body.List {
		clause, ok := stmt.(*ast.CaseClause)
		if !ok || clause.List == nil { // clause.List == nil is the "default:" arm
			continue
		}
		for _, expr := range clause.List {
			lit, ok := expr.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return false
			}
			found = true
		}
	}
	return found
}
