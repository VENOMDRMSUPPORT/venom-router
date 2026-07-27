package staticgate

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"regexp"
	"strconv"
	"strings"
)

// staticModelListThreshold is the minimum number of model-shaped string
// literals a single composite literal must contain before it is flagged.
// A threshold of one would flag ordinary code constantly (a single
// model-shaped string turns up in plenty of legitimate places — test
// fixtures, a lone default, a single documented example); three is
// conservative enough that only an actual LIST of models — the shape 04's
// "zero hardcoded model data" rule is worried about — trips it.
const staticModelListThreshold = 3

// modelIDCharset is the fixed, conservative shape rule for "looks like a
// model identifier": a lowercase token built from alphanumeric segments
// joined by '.', '-', or '/' (gpt-4o, claude-3-5-sonnet, openai/gpt-4,
// gemini-1.5-pro all match this shape). Combined with looksLikeModelID's
// additional digit and separator requirements below, this is deliberately
// narrow: the goal is to make "zero hardcoded model data" (docs/06 P3c
// gate) mechanically checkable without flagging ordinary code that merely
// happens to contain a hyphenated or slashed string.
var modelIDCharset = regexp.MustCompile(`^[a-z0-9]+(?:[.\-/][a-z0-9]+)*$`)

// looksLikeModelID reports whether s has the fixed shape of a model
// identifier: matches modelIDCharset, contains at least one '-' or '/'
// separator, AND contains at least one digit. All three conditions are
// required — this is what keeps ordinary lowercase words (operation
// names, statuses, single-segment identifiers) out of the check's way.
func looksLikeModelID(s string) bool {
	if !modelIDCharset.MatchString(s) {
		return false
	}
	if !strings.ContainsAny(s, "-/") {
		return false
	}
	for _, r := range s {
		if r >= '0' && r <= '9' {
			return true
		}
	}
	return false
}

// StaticModelListViolation describes one composite literal (slice,
// array, or map) whose string elements/keys look like a hardcoded list
// of model identifiers.
type StaticModelListViolation struct {
	File  string
	Line  int
	Count int
}

func (v StaticModelListViolation) String() string {
	return fmt.Sprintf("%s:%d: composite literal has %d model-shaped string literal(s) — models must never be hardcoded (04: zero hardcoded model data)", v.File, v.Line, v.Count)
}

// ErrStaticModelListScan wraps an I/O or parse failure during
// CheckNoStaticModelList's walk.
var ErrStaticModelListScan = errors.New("staticgate: no-static-model-list scan failed")

// CheckNoStaticModelList walks root (skipping the directories
// shouldSkipDir names) and, for every non-test .go file, reports every
// composite literal (slice, array, or map) with staticModelListThreshold
// or more string-literal elements/keys matching looksLikeModelID's fixed
// shape rule. Test files are exempt: fixtures and table-driven test data
// legitimately reference model ids without that being a routing-path
// hardcoding concern.
func CheckNoStaticModelList(root string) ([]StaticModelListViolation, error) {
	var violations []StaticModelListViolation
	fset := token.NewFileSet()

	err := walkRepo(root, func(path string, _ fs.DirEntry) error {
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return fmt.Errorf("parse %q: %w", path, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			count := countModelShapedElements(lit)
			if count >= staticModelListThreshold {
				pos := fset.Position(lit.Pos())
				violations = append(violations, StaticModelListViolation{File: pos.Filename, Line: pos.Line, Count: count})
			}
			return true
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrStaticModelListScan, err)
	}
	return violations, nil
}

// countModelShapedElements counts lit's direct elements (slice/array) or
// keys (map, via KeyValueExpr) that are model-shaped string literals.
func countModelShapedElements(lit *ast.CompositeLit) int {
	count := 0
	for _, elt := range lit.Elts {
		switch e := elt.(type) {
		case *ast.BasicLit:
			if isModelShapedBasicLit(e) {
				count++
			}
		case *ast.KeyValueExpr:
			if keyLit, ok := e.Key.(*ast.BasicLit); ok && isModelShapedBasicLit(keyLit) {
				count++
			}
		}
	}
	return count
}

func isModelShapedBasicLit(lit *ast.BasicLit) bool {
	if lit.Kind != token.STRING {
		return false
	}
	val, err := strconv.Unquote(lit.Value)
	if err != nil {
		return false
	}
	return looksLikeModelID(val)
}
