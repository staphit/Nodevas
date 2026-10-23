package agentsetup

import (
	"regexp"
	"strings"
)

// shell is the command interpreter an agent runs its hook commands through.
type shell int

const (
	// posixShell is sh/bash. Claude Code runs hooks through bash on every
	// platform (Git Bash on Windows), so its commands use this even there.
	posixShell shell = iota
	// powerShell is how Codex and Gemini CLI run hooks on Windows.
	powerShell
)

// shellFor picks the interpreter agent's hooks run under on goos.
func shellFor(agent, goos string) shell {
	if goos == "windows" && agent != Claude {
		return powerShell
	}
	return posixShell
}

var (
	posixPlain = regexp.MustCompile(`^[A-Za-z0-9/._:=@,+%-]+$`)
	psExePlain = regexp.MustCompile(`^[A-Za-z0-9\\:._-]+$`)
	psArgPlain = regexp.MustCompile(`^[A-Za-z0-9\\/:._=-]+$`)
)

// shellCmd renders `exe args…` for sh. A path that needs no quoting is left
// bare. On Windows, bash reads a backslash as an escape, so the path is
// written with forward slashes, which Windows accepts too.
func shellCmd(sh shell, goos, exe string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	if sh == powerShell {
		if psExePlain.MatchString(exe) {
			parts = append(parts, exe)
		} else {
			// A quoted path in PowerShell is a string, not a command: `&` runs it.
			parts = append(parts, "& "+psQuote(exe))
		}
		for _, a := range args {
			if psArgPlain.MatchString(a) {
				parts = append(parts, a)
			} else {
				parts = append(parts, psQuote(a))
			}
		}
		return strings.Join(parts, " ")
	}
	if goos == "windows" {
		exe = strings.ReplaceAll(exe, `\`, "/")
	}
	for _, a := range append([]string{exe}, args...) {
		if posixPlain.MatchString(a) {
			parts = append(parts, a)
		} else {
			parts = append(parts, "'"+strings.ReplaceAll(a, "'", `'\''`)+"'")
		}
	}
	return strings.Join(parts, " ")
}

func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// nodevasHook matches the binary name right before ` hook `, however the
// path in front of it was quoted, so a hook written by a nodevas installed
// elsewhere is still recognised as ours while another tool's
// `<other> hook claude …` is not.
var nodevasHook = regexp.MustCompile(`(?i)(^|[/\\'"\s&])nodevas(\.exe)?['"]?\s+hook\s`)

// ourHook reports whether command is a hook handler this program wrote for
// agent: it runs nodevas (this binary or one that moved) with `hook <agent>`.
func ourHook(command, agent, exe string) bool {
	if !strings.Contains(command, " hook "+agent+" ") && !strings.HasSuffix(command, " hook "+agent) {
		return false
	}
	return mentionsExe(command, exe) || nodevasHook.MatchString(command)
}

// mentionsExe reports whether text names exe in any spelling a config could
// hold it in: as is, with forward slashes, or JSON/TOML-escaped.
func mentionsExe(text, exe string) bool {
	if exe == "" {
		return false
	}
	for _, v := range []string{
		exe,
		strings.ReplaceAll(exe, `\`, "/"),
		strings.ReplaceAll(exe, `\`, `\\`),
	} {
		if strings.Contains(text, v) {
			return true
		}
	}
	return false
}
