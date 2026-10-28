package locale

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// spokenPackages are where the hub words what a person reads in the browser:
// errors and warnings the handlers send back, and titles the store writes.
var spokenPackages = []string{"../httpapi", "../store", "../project"}

// writtenInOneLanguage are the places a Chinese literal is not a sentence
// for the request's reader, keyed "file:function".
var writtenInOneLanguage = map[string]string{
	// Mail goes to a mailbox, not to the request's reader.
	"httpapi/system/accounts.go:inviteText":    "invitation mail, both languages",
	"httpapi/system/accounts.go:inviteSubject": "invitation mail, both languages",
	"httpapi/system/auth.go:otpSubject":        "sign-in code mail",
	"httpapi/system/auth.go:otpBody":           "sign-in code mail",
	"httpapi/system/stepup.go:stepUpSubject":   "step-up code mail",
	"httpapi/system/stepup.go:stepUpBody":      "step-up code mail",
	"httpapi/runners/link.go:mailRedeem":       "new-device mail",
	// Written into a new project once, picked by its creator's language.
	"project/project.go:eisenhowerTitles": "template titles per language",
}

// No sentence a reader gets back may be written only in Chinese: an English
// reader cannot read it. It goes through locale.Say or locale.Errorf, which
// carry both wordings.
func TestNoChineseOnlySentencesForTheReader(t *testing.T) {
	var found []string
	for _, root := range spokenPackages {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			found = append(found, chineseOnlyLiterals(t, path)...)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	sort.Strings(found)
	if len(found) > 0 {
		t.Fatalf("Chinese-only text a reader gets back (word it with locale.Say/Errorf):\n%s",
			strings.Join(found, "\n"))
	}
}

func chineseOnlyLiterals(t *testing.T, path string) []string {
	t.Helper()
	files := token.NewFileSet()
	file, err := parser.ParseFile(files, path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	relative := filepath.ToSlash(strings.TrimPrefix(filepath.ToSlash(path), "../"))
	var found []string
	paired := map[*ast.BasicLit]bool{}
	var visit func(node ast.Node, owner string, translated bool)
	visit = func(node ast.Node, owner string, translated bool) {
		ast.Inspect(node, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CallExpr:
				if selector, ok := n.Fun.(*ast.SelectorExpr); ok {
					if pkg, ok := selector.X.(*ast.Ident); ok && pkg.Name == "locale" {
						for _, arg := range n.Args {
							visit(arg, owner, true)
						}
						visit(n.Fun, owner, translated)
						return false
					}
				}
				// An English wording followed by its Chinese one, as the
				// store's warn(english, chinese, args...) takes them.
				for i := 1; i < len(n.Args); i++ {
					if english, ok := stringLiteral(n.Args[i-1]); ok && !hasHan(english) {
						if chinese, ok := n.Args[i].(*ast.BasicLit); ok {
							paired[chinese] = true
						}
					}
				}
			case *ast.BasicLit:
				if n.Kind != token.STRING || translated || paired[n] {
					return false
				}
				value, err := strconv.Unquote(n.Value)
				if err != nil || !hasHan(value) {
					return false
				}
				if _, ok := writtenInOneLanguage[relative+":"+owner]; ok {
					return false
				}
				found = append(found, files.Position(n.Pos()).String()+": "+value)
			}
			return true
		})
	}
	for _, declaration := range file.Decls {
		switch declaration := declaration.(type) {
		case *ast.FuncDecl:
			visit(declaration, declaration.Name.Name, false)
		case *ast.GenDecl:
			for _, spec := range declaration.Specs {
				owner := ""
				if value, ok := spec.(*ast.ValueSpec); ok && len(value.Names) > 0 {
					owner = value.Names[0].Name
				}
				visit(spec, owner, false)
			}
		}
	}
	return found
}

func stringLiteral(expression ast.Expr) (string, bool) {
	literal, ok := expression.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(literal.Value)
	return value, err == nil
}

func hasHan(text string) bool {
	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}
