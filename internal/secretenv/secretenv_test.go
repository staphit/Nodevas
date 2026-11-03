package secretenv_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"nodevas/internal/dbconn"
	"nodevas/internal/secretenv"
)

func TestWithoutDropsEveryHubSecret(t *testing.T) {
	var environ []string
	for _, name := range secretenv.Names() {
		environ = append(environ, name+"=secret-"+name)
		// Windows variable names ignore case: a lower-case spelling is the
		// same variable there.
		environ = append(environ, strings.ToLower(name)+"=secret-lower")
	}
	environ = append(environ, "PATH=/usr/bin", "NODEVAS_COPILOT_API_KEY=runner-copilot", "HOME=/home/a")
	got := secretenv.Without(environ)
	for _, kv := range got {
		if strings.Contains(kv, "secret-") {
			t.Errorf("Without kept %q", kv)
		}
	}
	for _, want := range []string{"PATH=/usr/bin", "NODEVAS_COPILOT_API_KEY=runner-copilot", "HOME=/home/a"} {
		if !slices.Contains(got, want) {
			t.Errorf("Without dropped %q: %q", want, got)
		}
	}
}

func TestWithoutKeepsAnEntryWithNoEquals(t *testing.T) {
	// Windows keeps "=C:=C:\dir" entries for drive working directories.
	got := secretenv.Without([]string{"=C:=C:\\work", "NODEVAS_SECRET_KEY_FILE=/run/secrets/key"})
	if len(got) != 1 || got[0] != "=C:=C:\\work" {
		t.Fatalf("Without = %q", got)
	}
}

// secretLooking is a variable name that, by its spelling, holds or points
// at a credential.
var secretLooking = regexp.MustCompile(`KEY|SECRET|PASSWORD|PASS|TOKEN|DATABASE_URL|CREDENTIAL|DSN`)

// notTheHubs are secret-looking names in the source that are not secrets
// the hub reads, each with the reason.
var notTheHubs = map[string]string{
	"NODEVAS_ADVISOR_API_KEY":      "the runner's own advisor key; the hub never reads it",
	"NODEVAS_REQUIRE_DATABASE_URL": "an on/off switch, not a credential",
}

// TestEverySecretTheHubReadsIsWithheld walks every non-test Go file for the
// environment variables the code names, so a secret added later is either
// on the list or explained here.
func TestEverySecretTheHubReadsIsWithheld(t *testing.T) {
	root := repoRoot(t)
	names := map[string]string{}
	for _, dir := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" || d.Name() == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			ast.Inspect(file, func(n ast.Node) bool {
				switch node := n.(type) {
				case *ast.BasicLit:
					if value, ok := literal(node); ok && envName(value) {
						names[value] = rel
					}
				case *ast.CallExpr:
					// serve's secretEnvironment("X") also reads X_FILE.
					if ident, ok := node.Fun.(*ast.Ident); ok && ident.Name == "secretEnvironment" && len(node.Args) == 1 {
						if lit, ok := node.Args[0].(*ast.BasicLit); ok {
							if value, ok := literal(lit); ok {
								names[value] = rel
								names[value+"_FILE"] = rel
							}
						}
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	names[dbconn.URLEnv] = "internal/dbconn"
	names[dbconn.URLFileEnv] = "internal/dbconn"
	if len(names) < 20 {
		t.Fatalf("the walk found only %d variable names; is it reading the source?", len(names))
	}
	for _, want := range []string{"NODEVAS_SECRET_KEY_FILE", "NODEVAS_SMTP_PASSWORD_FILE", "NODEVAS_COPILOT_API_KEY_FILE"} {
		if _, ok := names[want]; !ok {
			t.Errorf("the walk did not find %s", want)
		}
	}
	for name, where := range names {
		if !secretLooking.MatchString(name) {
			continue
		}
		if secretenv.IsHubSecret(name) || slices.Contains(secretenv.SharedWithRunner, name) {
			continue
		}
		if _, ok := notTheHubs[name]; ok {
			continue
		}
		t.Errorf("%s (named in %s) looks like a secret: add it to hubSecrets, SharedWithRunner or notTheHubs", name, where)
	}
}

func literal(lit *ast.BasicLit) (string, bool) {
	if lit.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(lit.Value)
	return value, err == nil
}

var envNamePattern = regexp.MustCompile(`^(NODEVAS_[A-Z0-9_]+|PG[A-Z]+|[A-Z]+_API_KEY)$`)

func envName(s string) bool { return envNamePattern.MatchString(s) }

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test")
		}
		dir = parent
	}
}
