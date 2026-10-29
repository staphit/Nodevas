package locale

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// readerPackages are where an error a person may read is made: the handlers,
// the middleware in front of them and what they call. An error made here is
// worded in both languages (locale.Errorf), so the handler that answers it can
// give it to its reader in theirs.
var readerPackages = []string{
	"../httpapi", "../server", "../store", "../project", "../runs", "../runners",
	"../members", "../export", "../engine", "../auth", "../remote", "../notify",
	"../usage", "../analytics", "../autopilot", "../gitrepo", "../document",
}

// machineErrors are the errors made in readerPackages that no person reads,
// keyed "file:function" (the function, or the variable an error is declared
// as), with why.
var machineErrors = map[string]string{}

// verbs are fmt's verbs: a format that is only verbs and punctuation ("%w:
// %s") wraps words made elsewhere and says nothing of its own.
var verbs = regexp.MustCompile(`%[-+# 0-9.*\[\]]*[a-zA-Z%]`)

// words reports whether format says something of its own.
func words(format string) bool {
	rest := verbs.ReplaceAllString(format, "")
	letters := 0
	for _, r := range rest {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			letters++
			if letters > 1 {
				return true
			}
		} else {
			letters = 0
		}
	}
	return false
}

// No error a reader may get back is worded in English only: errors.New and
// fmt.Errorf make one, locale.Errorf makes one in both languages.
func TestReaderErrorsGoThroughTheLocalizer(t *testing.T) {
	var found []string
	used := map[string]bool{}
	for _, root := range readerPackages {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			found = append(found, englishOnlyErrors(t, path, used)...)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	sort.Strings(found)
	if len(found) > 0 {
		t.Fatalf("%d errors a reader may get back in English only (word them with locale.Errorf, "+
			"or list them in machineErrors if no person reads them):\n%s", len(found), strings.Join(found, "\n"))
	}
	for key := range machineErrors {
		if !used[key] {
			t.Errorf("machineErrors lists %s, which makes no English-only error any more", key)
		}
	}
}

func englishOnlyErrors(t *testing.T, path string, used map[string]bool) []string {
	t.Helper()
	files := token.NewFileSet()
	file, err := parser.ParseFile(files, path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	relative := filepath.ToSlash(strings.TrimPrefix(filepath.ToSlash(path), "../"))
	var found []string
	check := func(node ast.Node, owner string) {
		ast.Inspect(node, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := selector.X.(*ast.Ident)
			if !ok {
				return true
			}
			if name := pkg.Name + "." + selector.Sel.Name; name != "errors.New" && name != "fmt.Errorf" {
				return true
			}
			format, literal := stringLiteral(call.Args[0])
			if literal && !words(format) {
				return true
			}
			key := relative + ":" + owner
			if _, ok := machineErrors[key]; ok {
				used[key] = true
				return true
			}
			if !literal {
				format = "(a format made at run time)"
			}
			found = append(found, files.Position(call.Pos()).String()+" "+owner+": "+strconv.Quote(format))
			return true
		})
	}
	for _, declaration := range file.Decls {
		switch declaration := declaration.(type) {
		case *ast.FuncDecl:
			check(declaration, declaration.Name.Name)
		case *ast.GenDecl:
			for _, spec := range declaration.Specs {
				owner := ""
				if value, ok := spec.(*ast.ValueSpec); ok && len(value.Names) > 0 {
					owner = value.Names[0].Name
				}
				check(spec, owner)
			}
		}
	}
	return found
}

func TestWordsTellsASentenceFromAWrapper(t *testing.T) {
	for format, want := range map[string]bool{
		"%w: %s":            false,
		"%s: %w":            false,
		"%w (%s)":           false,
		"%[2]s → %[1]q":     false,
		"node %q not found": true,
		"%w: no expiry":     true,
	} {
		if got := words(format); got != want {
			t.Errorf("words(%q) = %v, want %v", format, got, want)
		}
	}
}
