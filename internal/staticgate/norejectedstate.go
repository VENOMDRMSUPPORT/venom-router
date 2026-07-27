package staticgate

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// walkRepo and shouldSkipDir below are the repo-wide walk helper shared
// by CheckNoRejectedState and CheckNoStaticModelList.

// skipDirNames are directory base names this repo's static checks never
// descend into: node_modules (dashboard's dependency tree), dashboard
// and Design_System (frozen, non-Go trees), and vendor/third_party — the
// two vendoring conventions this repo actually uses (third_party/bifrost
// is a vendored AI-gateway SDK; its own source legitimately contains real
// model identifiers and provider-facing strings that are not this
// project's hardcoding).
var skipDirNames = map[string]bool{
	"node_modules":  true,
	"dashboard":     true,
	"Design_System": true,
	"vendor":        true,
	"third_party":   true,
}

// shouldSkipDir reports whether a directory (by base name) must never be
// descended into: an explicit skip name, or any dot-prefixed tooling
// directory (.git, .github, .hive, .omo, .superpowers, .codegraph, ...).
func shouldSkipDir(name string) bool {
	if skipDirNames[name] {
		return true
	}
	return strings.HasPrefix(name, ".")
}

// walkRepo recursively visits every non-skipped, non-symlinked file
// under root, in filepath.WalkDir's deterministic lexical order.
func walkRepo(root string, visit func(path string, d fs.DirEntry) error) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if path != root && shouldSkipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		return visit(path, d)
	})
}

// RejectedStateViolation describes one occurrence of the literal string
// "rejected" found where 04 §5 says no such certification/probe state
// may ever appear ("There is no rejected state... no rejected state
// anywhere in code/schema/API").
type RejectedStateViolation struct {
	File string
	Line int
}

func (v RejectedStateViolation) String() string {
	return fmt.Sprintf("%s:%d: the literal \"rejected\" is not a legal certification/probe state (04 §5 — there is no rejected state)", v.File, v.Line)
}

// ErrRejectedStateScan wraps an I/O or parse failure during
// CheckNoRejectedState's walk.
var ErrRejectedStateScan = errors.New("staticgate: no-rejected-state scan failed")

// CheckNoRejectedState walks root (skipping the directories
// shouldSkipDir names) and reports every occurrence of the literal
// "rejected": for non-test .go files, every STRING LITERAL (via
// go/parser's AST) whose unquoted value is exactly "rejected"; for every
// .sql file, every line containing the SQL token 'rejected'.
//
// This precision is mandatory. The English word "rejected" appears
// legitimately throughout this repo — in comments, identifiers, and
// unrelated domains (a rejected insert, a rejected credential, a
// rejected quota transition). A naive text search over file contents
// would flag all of that noise and would be disabled within a week;
// scanning STRING LITERALS via the AST — never comments, never
// identifiers — is what keeps this check honest enough to stay on.
//
// The returned slice's order follows the underlying filesystem walk
// (deterministic given a fixed tree) and, within one file, source line
// order — never the output of ranging over a map.
func CheckNoRejectedState(root string) ([]RejectedStateViolation, error) {
	var violations []RejectedStateViolation
	fset := token.NewFileSet()

	err := walkRepo(root, func(path string, _ fs.DirEntry) error {
		switch {
		case strings.HasSuffix(path, ".sql"):
			return scanSQLForRejected(path, &violations)
		case strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go"):
			return scanGoForRejectedLiteral(fset, path, &violations)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRejectedStateScan, err)
	}
	return violations, nil
}

// targetLiteral is the exact value scanGoForRejectedLiteral compares
// every string literal against, assembled from parts rather than written
// as a bare literal: this file is itself a non-test .go file under the
// same repo tree the check scans, and a literal "rejected" written here
// would make this file a permanent, unavoidable self-match.
var targetLiteral = "reject" + "ed"

// scanGoForRejectedLiteral parses path and reports every *ast.BasicLit
// string literal whose unquoted value is exactly targetLiteral. Comments
// and identifiers are never visited as string literals by go/ast, so
// they are structurally excluded — not filtered after the fact.
func scanGoForRejectedLiteral(fset *token.FileSet, path string, out *[]RejectedStateViolation) error {
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return fmt.Errorf("parse %q: %w", path, err)
	}
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		val, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		if val == targetLiteral {
			pos := fset.Position(lit.Pos())
			*out = append(*out, RejectedStateViolation{File: pos.Filename, Line: pos.Line})
		}
		return true
	})
	return nil
}

// scanSQLForRejected reports every line of path containing the SQL
// single-quoted token 'rejected'.
func scanSQLForRejected(path string, out *[]RejectedStateViolation) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %q: %w", path, err)
	}
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		if strings.Contains(line, "'rejected'") {
			*out = append(*out, RejectedStateViolation{File: path, Line: i + 1})
		}
	}
	return nil
}
