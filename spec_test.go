package rebuild_test

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// inScope mirrors RB-1 in specs/rebuild.md.
var inScope = func() []string {
	var ids []string
	add := func(prefix string, ns ...int) {
		for _, n := range ns {
			ids = append(ids, fmt.Sprintf("%s-%d", prefix, n))
		}
	}
	span := func(a, b int) []int {
		var out []int
		for i := a; i <= b; i++ {
			out = append(out, i)
		}
		return out
	}
	add("AUTH", 1, 2, 3, 4, 6, 13)
	add("AVL", append(span(1, 8), 10)...)
	add("EVT", append(span(1, 8), 10, 11, 13, 14, 16)...)
	add("SLT", append(append([]int{1, 2}, span(4, 9)...), 12, 13, 14, 15, 17, 18)...)
	add("BKG", append(span(1, 13), 15, 18)...)
	add("PUB", 1, 2, 4, 5, 6)
	return ids
}()

// TestSpecCoverage is RB-1/RB-2: every in-scope ID exists upstream and has a test named Test<PREFIX>_<n>.
func TestSpecCoverage(t *testing.T) {
	rows := map[string]string{}
	rowRe := regexp.MustCompile(`(?m)^\| ([A-Z]+-\d+) \| (.+?) \| (.+?) \|$`)
	files, _ := filepath.Glob("specs/upstream/*.md")
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range rowRe.FindAllStringSubmatch(string(b), -1) {
			rows[m[1]] = m[2]
		}
	}
	tests := map[string]bool{}
	filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil || !strings.HasSuffix(path, "_test.go") {
			return err
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		for name := range f.Scope.Objects {
			tests[name] = true
		}
		return nil
	})
	for _, id := range inScope {
		req, ok := rows[id]
		if !ok || strings.HasPrefix(req, "Removed") {
			t.Errorf("%s: not an upstream requirement", id)
		}
		if name := "Test" + strings.ReplaceAll(id, "-", "_"); !tests[name] {
			t.Errorf("%s: no test %s", id, name)
		}
	}
}
