package adapter

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Batch shims. npm installs codex and gemini on Windows as .cmd files, and a
// batch file can only run under cmd.exe, which re-parses the command line by
// its own rules: % expands variables (even inside quotes), & | < > ^ are
// operators outside quotes, and a quoted program path plus a quoted last
// argument loses its outer quotes to /c's quote stripping. The shim then
// hands %* on to node, which cmd.exe parses once more.
//
// So for a .cmd/.bat binary the runner (wrapBatch, Windows only) starts
// cmd.exe itself with an explicit command line, `cmd.exe /d /v:off /s /c
// "<program> <args>"`: /s strips exactly the outer pair of quotes, /d skips
// AutoRun commands, /v:off keeps ! literal. Every argument is quoted the
// usual Windows way, and any argument holding a character cmd.exe would act
// on is refused rather than escaped: carets that are right for one parse are
// wrong for the next, and none of these arguments needs such characters.
// Prompts never go on the command line; they go on stdin.

// batchUnsafe are the characters refused in an argument to a batch shim.
const batchUnsafe = "\"%^&|<>\r\n\x00"

func isBatch(binary string) bool {
	switch strings.ToLower(filepath.Ext(binary)) {
	case ".cmd", ".bat":
		return true
	}
	return false
}

// checkBatchArgs refuses arguments cmd.exe would not pass on unchanged.
func checkBatchArgs(args []string) error {
	for _, arg := range args {
		if i := strings.IndexAny(arg, batchUnsafe); i >= 0 {
			return fmt.Errorf("argument %q holds %q, which the CLI's .cmd launcher cannot pass on safely; "+
				"rename the path or setting that contains it", arg, arg[i:i+1])
		}
	}
	return nil
}

// tomlString is s as a TOML string for a `-c key=value` override: a literal
// string ('...', no escapes, so Windows paths stay readable) when it can be,
// otherwise a basic string with escapes.
func tomlString(s string) string {
	if !strings.Contains(s, "'") && !hasControl(s) {
		return "'" + s + "'"
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04X`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// tomlArray is a TOML array of strings.
func tomlArray(items []string) string {
	quoted := make([]string, len(items))
	for i, item := range items {
		quoted[i] = tomlString(item)
	}
	return "[" + strings.Join(quoted, ",") + "]"
}

func hasControl(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}
