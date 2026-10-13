// Package i18ntest checks translation catalogs against the code: every
// marked text has a translation, no translation is left over, format verbs
// match, and no user-facing text bypasses T.
package i18ntest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/napadailo/voip-tui/i18n"
)

// Messages collects the texts the Go files in dirs mark for translation:
// arguments of T, Tf, N (bare or as i18n.X/tui.X), formats of the Infof,
// Warnf and Errf status-line helpers, and the keys of [2]string key/value
// rows (a leading "#" marks a section title and is dropped).
func Messages(t *testing.T, dirs ...string) map[string]string {
	t.Helper()
	found := map[string]string{}
	add := func(fset *token.FileSet, e ast.Expr) {
		lit, ok := e.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return
		}
		s, err := strconv.Unquote(lit.Value)
		if err != nil || strings.TrimSpace(s) == "" {
			return
		}
		found[strings.TrimPrefix(s, "#")] = fset.Position(lit.Pos()).String()
	}
	isKV := func(e ast.Expr) bool { // [2]string
		a, ok := e.(*ast.ArrayType)
		if !ok || a.Len == nil {
			return false
		}
		l, ok := a.Len.(*ast.BasicLit)
		id, ok2 := a.Elt.(*ast.Ident)
		return ok && ok2 && l.Value == "2" && id.Name == "string"
	}
	for _, dir := range dirs {
		forFiles(t, dir, func(fset *token.FileSet, file *ast.File) {
			ast.Inspect(file, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.CallExpr:
					name := ""
					switch fn := n.Fun.(type) {
					case *ast.Ident:
						name = fn.Name
					case *ast.SelectorExpr:
						name = fn.Sel.Name
						if x, ok := fn.X.(*ast.Ident); ok && x.Name != "i18n" && x.Name != "tui" && (name == "T" || name == "Tf" || name == "N") {
							name = ""
						}
					}
					switch name {
					case "T", "Tf", "N", "Infof", "Warnf", "Errf":
						if len(n.Args) > 0 {
							add(fset, n.Args[0])
						}
					}
				case *ast.CompositeLit:
					if isKV(n.Type) && len(n.Elts) == 2 {
						add(fset, n.Elts[0])
					}
					if a, ok := n.Type.(*ast.ArrayType); ok && isKV(a.Elt) {
						for _, e := range n.Elts {
							if row, ok := e.(*ast.CompositeLit); ok && len(row.Elts) == 2 {
								add(fset, row.Elts[0])
							}
						}
					}
				}
				return true
			})
		})
	}
	return found
}

func forFiles(t *testing.T, dir string, f func(*token.FileSet, *ast.File)) {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(dir, "*.go"))
	if len(files) == 0 {
		t.Fatalf("no Go files in %s", dir)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") || strings.HasSuffix(name, "_gen.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		f(fset, file)
	}
}

// Check verifies the catalogs a package registers (own, by language)
// against the messages of its code: each message must be translated, by
// own or by a catalog another package registered (the tui framework), and
// each own entry must be used.
func Check(t *testing.T, msgs map[string]string, own map[string]map[string]string) {
	t.Helper()
	if len(msgs) == 0 {
		t.Fatal("no messages found, the scanner is broken")
	}
	for lang, cat := range own {
		var missing, unused []string
		for m, pos := range msgs {
			if _, ok := cat[m]; !ok && !i18n.Has(lang, m) {
				missing = append(missing, strconv.Quote(m)+"  // "+pos)
			}
		}
		for k := range cat {
			if _, ok := msgs[k]; !ok {
				unused = append(unused, strconv.Quote(k))
			}
		}
		sort.Strings(missing)
		sort.Strings(unused)
		for _, m := range missing {
			t.Errorf("%s: no translation: %s", lang, m)
		}
		for _, k := range unused {
			t.Errorf("%s: translation of text the code does not use: %s", lang, k)
		}
	}
	CheckVerbs(t, own)
}

var verbRe = regexp.MustCompile(`%[-+# 0-9.*]*[a-zA-Z%]`)

// CheckVerbs fails on translations whose format verbs differ from the
// English text: fmt would print garbage instead of failing.
func CheckVerbs(t *testing.T, catalogs map[string]map[string]string) {
	t.Helper()
	for lang, cat := range catalogs {
		for en, tr := range cat {
			a, b := verbRe.FindAllString(en, -1), verbRe.FindAllString(tr, -1)
			if strings.Join(a, " ") != strings.Join(b, " ") {
				t.Errorf("%s: %q: verbs %v, translation %q has %v", lang, en, a, tr, b)
			}
		}
	}
}

// CheckMarked finds messages written straight into errors and the status
// line without T: they would stay English in every language. allow lists
// technical formats that stay as they are.
func CheckMarked(t *testing.T, allow map[string]bool, dirs ...string) {
	t.Helper()
	for _, dir := range dirs {
		forFiles(t, dir, func(fset *token.FileSet, file *ast.File) {
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) == 0 {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkg, _ := sel.X.(*ast.Ident)
				switch {
				case pkg != nil && pkg.Name == "errors" && sel.Sel.Name == "New",
					pkg != nil && pkg.Name == "fmt" && sel.Sel.Name == "Errorf",
					sel.Sel.Name == "Info" || sel.Sel.Name == "Warn" || sel.Sel.Name == "SetError":
				default:
					return true
				}
				lit, ok := call.Args[0].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				s, _ := strconv.Unquote(lit.Value)
				if !allow[s] {
					t.Errorf("%s: %q is shown without T()", fset.Position(lit.Pos()), s)
				}
				return true
			})
		})
	}
}
