package runs

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"nodevas/internal/redact"
	"nodevas/internal/runnerwire"
)

const (
	// compareCutMarker matches web/src/i18n.ts "runDialog.cut" in English (§C.4.2).
	compareCutMarker = "(longer text was cut)"
	// compareSummaryCap caps each entry's summary in the reviewer prompt (§C.4.2).
	compareSummaryCap = 2 << 10
	// comparePromptReserve holds room for the task, entry metadata and scoring instructions (§C.4.2).
	comparePromptReserve = 8 << 10
	// compareTaskCap caps the task section inside the prompt reserve.
	compareTaskCap = 4 << 10
	// compareMaxFilesPerEntry caps the file summary lines listed per entry.
	compareMaxFilesPerEntry = 50
	// compareMaxWhyRunes caps each parsed score's explanation (§C.4.2).
	compareMaxWhyRunes = 300
	// compareInstruction tells the reviewer how to score and format the final JSON block (§C.4.2).
	compareInstruction = `Score each entry 0-10 for correctness, completeness, scope discipline, tests; then pick one. End with a fenced json block: {"scores":[{"entry":"A","score":7,"why":"…"}],"pick":"A"}.`
)

// CompareEntry is one succeeded contest entry passed to BuildComparePrompt.
// It deliberately omits agent and model names so the reviewer scores entries
// anonymously by label (§C.4.2).
type CompareEntry struct {
	Label   string
	Branch  string
	Summary string
	Changes *runnerwire.RunChanges
}

// BuildComparePrompt builds the read-only compare reviewer prompt for a
// contest (§C.4.2):
//   - the task;
//   - entries under their labels only (A, B, C), rotated deterministically by
//     contestID to reduce position bias;
//   - for each entry: branch name, redacted summary (<= 2 KiB), changed files
//     and +/- counts (excluding secret paths), and cleaned patch (secret-file
//     hunks dropped, rest passed through redact.Output), each cut to fit
//     (MaxPromptBytes - 8 KiB) / n and MaxPromptBytes overall;
//   - the scoring and fenced-JSON instruction.
func BuildComparePrompt(contestID, task string, entries []CompareEntry) string {
	ordered := rotateCompareEntries(contestID, entries)
	n := len(ordered)

	taskText := redact.Output(strings.TrimSpace(task))
	taskText = cutBytes(taskText, compareTaskCap)

	head := "# Contest review\n\n## Task\n" + taskText + "\n"
	tail := "\n## Instructions\n" + compareInstruction + "\n"
	if n == 0 {
		return Truncate(head+tail, runnerwire.MaxPromptBytes)
	}

	type preparedEntry struct {
		meta  string
		patch string
	}
	prepared := make([]preparedEntry, n)
	fixedBytes := len(head) + len(tail)
	for i, e := range ordered {
		meta := formatCompareEntryMeta(e)
		patch := ""
		if e.Changes != nil && e.Changes.Patch != "" {
			patch = CleanComparePatch(e.Changes.Patch)
		}
		prepared[i] = preparedEntry{meta: meta, patch: patch}
		fixedBytes += len(meta)
		if patch != "" {
			fixedBytes += len("Patch:\n```diff\n\n```\n")
		}
	}

	// Spec cap: each patch gets at most (MaxPromptBytes - 8 KiB) / n,
	// tightened further if fixed metadata exceeds the 8 KiB reserve.
	patchCap := (runnerwire.MaxPromptBytes - comparePromptReserve) / n
	if avail := (runnerwire.MaxPromptBytes - fixedBytes) / n; avail < patchCap {
		patchCap = max(avail, 0)
	}

	var b strings.Builder
	b.WriteString(head)
	for _, p := range prepared {
		b.WriteString(p.meta)
		if p.patch != "" && patchCap > 0 {
			b.WriteString("Patch:\n```diff\n")
			b.WriteString(cutBytes(p.patch, patchCap))
			b.WriteString("\n```\n")
		}
	}

	// Ironclad guarantee: never exceed MaxPromptBytes, and always keep tail.
	body := b.String()
	maxBody := runnerwire.MaxPromptBytes - len(tail)
	if len(body) > maxBody {
		body = cutBytes(body, maxBody)
	}
	return Truncate(body+tail, runnerwire.MaxPromptBytes)
}

func rotateCompareEntries(contestID string, entries []CompareEntry) []CompareEntry {
	if len(entries) == 0 {
		return nil
	}
	sorted := slices.Clone(entries)
	slices.SortStableFunc(sorted, func(a, b CompareEntry) int {
		return strings.Compare(a.Label, b.Label)
	})
	sum := sha256.Sum256([]byte(contestID))
	offset := int(binary.BigEndian.Uint64(sum[:8]) % uint64(len(sorted)))
	if offset == 0 {
		return sorted
	}
	rotated := make([]CompareEntry, len(sorted))
	for i := range sorted {
		rotated[i] = sorted[(i+offset)%len(sorted)]
	}
	return rotated
}

func formatCompareEntryMeta(e CompareEntry) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n## Entry %s\n", e.Label)
	if branch := strings.TrimSpace(e.Branch); branch != "" {
		fmt.Fprintf(&b, "Branch: %s\n", branch)
	}
	if summary := strings.TrimSpace(redact.Output(e.Summary)); summary != "" {
		fmt.Fprintf(&b, "Summary:\n%s\n", cutBytes(summary, compareSummaryCap))
	}
	if e.Changes != nil {
		var kept []runnerwire.FileChange
		addTotal, delTotal := 0, 0
		for _, f := range e.Changes.Files {
			if redact.SecretPath(f.Path) || (f.OldPath != "" && redact.SecretPath(f.OldPath)) {
				continue
			}
			kept = append(kept, f)
			addTotal += f.Additions
			delTotal += f.Deletions
		}
		if len(kept) > 0 {
			fmt.Fprintf(&b, "Changes: %d files (+%d/-%d)\n", len(kept), addTotal, delTotal)
			limit := min(len(kept), compareMaxFilesPerEntry)
			for _, f := range kept[:limit] {
				fmt.Fprintf(&b, "- %s (%s, +%d/-%d)\n", f.Path, f.Status, f.Additions, f.Deletions)
			}
			if len(kept) > limit {
				fmt.Fprintf(&b, "- %s (+%d more files)\n", compareCutMarker, len(kept)-limit)
			}
		} else if e.Changes.Additions > 0 || e.Changes.Deletions > 0 {
			fmt.Fprintf(&b, "Changes: +%d/-%d\n", e.Changes.Additions, e.Changes.Deletions)
		}
	}
	return b.String()
}

func cutBytes(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	suffix := "\n" + compareCutMarker
	if maxBytes <= len(suffix) {
		return Truncate(compareCutMarker, maxBytes)
	}
	return strings.TrimRight(Truncate(s, maxBytes-len(suffix)), "\r\n") + suffix
}

// CleanComparePatch drops every file section whose path matches
// redact.SecretPath and passes the remaining diff through redact.Output
// (§C.4.2, §C.10).
func CleanComparePatch(patch string) string {
	if strings.TrimSpace(patch) == "" {
		return ""
	}
	sections := splitDiffSections(patch)
	var kept []string
	for _, sec := range sections {
		if diffSectionHasSecretPath(sec) {
			continue
		}
		kept = append(kept, sec)
	}
	if len(kept) == 0 {
		return ""
	}
	return redact.Output(strings.Join(kept, ""))
}

// splitDiffSections splits a unified git diff into per-file sections starting
// at each "diff --git " header line.
func splitDiffSections(patch string) []string {
	var sections []string
	start := 0
	for i := 0; i < len(patch); {
		if i > start && (i == 0 || patch[i-1] == '\n') && strings.HasPrefix(patch[i:], "diff --git ") {
			sections = append(sections, patch[start:i])
			start = i
		}
		nl := strings.IndexByte(patch[i:], '\n')
		if nl < 0 {
			break
		}
		i += nl + 1
	}
	if start < len(patch) {
		sections = append(sections, patch[start:])
	}
	return sections
}

func diffSectionHasSecretPath(section string) bool {
	lines := strings.Split(section, "\n")
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			for _, p := range parseDiffGitPaths(line[len("diff --git "):]) {
				if redact.SecretPath(p) {
					return true
				}
			}
		case strings.HasPrefix(line, "--- "):
			if p := cleanDiffHeaderPath(line[4:]); p != "" && redact.SecretPath(p) {
				return true
			}
		case strings.HasPrefix(line, "+++ "):
			if p := cleanDiffHeaderPath(line[4:]); p != "" && redact.SecretPath(p) {
				return true
			}
		case strings.HasPrefix(line, "rename from "):
			if p := cleanDiffHeaderPath(line[len("rename from "):]); p != "" && redact.SecretPath(p) {
				return true
			}
		case strings.HasPrefix(line, "rename to "):
			if p := cleanDiffHeaderPath(line[len("rename to "):]); p != "" && redact.SecretPath(p) {
				return true
			}
		case strings.HasPrefix(line, "@@ "):
			// Past the file headers; hunk lines start here.
			return false
		}
	}
	return false
}

func parseDiffGitPaths(rest string) []string {
	rest = strings.TrimSpace(rest)
	var out []string
	// Handle both unquoted "a/foo b/bar" and quoted "\"a/foo bar\" \"b/foo bar\"".
	for rest != "" {
		var tok string
		if rest[0] == '"' {
			end := 1
			for end < len(rest) {
				if rest[end] == '\\' && end+1 < len(rest) {
					end += 2
					continue
				}
				if rest[end] == '"' {
					end++
					break
				}
				end++
			}
			tok = rest[:end]
			rest = strings.TrimLeft(rest[end:], " \t")
		} else {
			sp := strings.IndexAny(rest, " \t")
			if sp < 0 {
				tok = rest
				rest = ""
			} else {
				tok = rest[:sp]
				rest = strings.TrimLeft(rest[sp:], " \t")
			}
		}
		if p := cleanDiffHeaderPath(tok); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func cleanDiffHeaderPath(raw string) string {
	s := strings.TrimSpace(raw)
	if tab := strings.IndexByte(s, '\t'); tab >= 0 {
		s = s[:tab]
	}
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		if unq, err := strconv.Unquote(s); err == nil {
			s = unq
		} else {
			s = s[1 : len(s)-1]
		}
	}
	if s == "" || s == "/dev/null" {
		return ""
	}
	if strings.HasPrefix(s, "a/") || strings.HasPrefix(s, "b/") {
		s = s[2:]
	}
	return s
}

// ParseScores extracts and validates the reviewer's final fenced JSON block
// from summary (§C.4.2):
//   - reads only the last fenced ```json block;
//   - every entry label and the pick must be in allowedLabels, with no duplicate labels;
//   - scores must be integers in [0, 10];
//   - each why is passed through redact.Output and capped at 300 runes.
//
// Anything invalid returns nil.
func ParseScores(summary string, allowedLabels []string) *runnerwire.ContestScores {
	if len(allowedLabels) == 0 {
		return nil
	}
	block, ok := lastFencedJSONBlock(summary)
	if !ok {
		return nil
	}
	var raw struct {
		Scores []struct {
			Entry string          `json:"entry"`
			Score json.RawMessage `json:"score"`
			Why   string          `json:"why"`
		} `json:"scores"`
		Pick string `json:"pick"`
	}
	if err := json.Unmarshal([]byte(block), &raw); err != nil {
		return nil
	}
	if len(raw.Scores) == 0 {
		return nil
	}
	allowed := make(map[string]bool, len(allowedLabels))
	for _, lbl := range allowedLabels {
		if lbl != "" {
			allowed[lbl] = true
		}
	}
	pick := strings.TrimSpace(raw.Pick)
	if !allowed[pick] {
		return nil
	}
	seen := make(map[string]bool, len(raw.Scores))
	scores := make([]runnerwire.ContestScore, 0, len(raw.Scores))
	for _, item := range raw.Scores {
		entry := strings.TrimSpace(item.Entry)
		if !allowed[entry] || seen[entry] {
			return nil
		}
		seen[entry] = true
		val, err := strconv.Atoi(strings.TrimSpace(string(item.Score)))
		if err != nil || val < 0 || val > 10 {
			return nil
		}
		why := cleanScoreWhy(item.Why)
		scores = append(scores, runnerwire.ContestScore{
			Entry: entry,
			Score: val,
			Why:   why,
		})
	}
	return &runnerwire.ContestScores{
		Scores: scores,
		Pick:   pick,
	}
}

func lastFencedJSONBlock(text string) (string, bool) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	inFence := false
	isJSON := false
	var buf strings.Builder
	lastBlock := ""
	found := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !inFence {
			if strings.HasPrefix(trimmed, "```") {
				inFence = true
				lang := strings.TrimSpace(trimmed[3:])
				isJSON = strings.EqualFold(lang, "json")
				buf.Reset()
			}
			continue
		}
		if trimmed == "```" || (strings.HasPrefix(trimmed, "```") && strings.Trim(trimmed, "`") == "") {
			if isJSON {
				lastBlock = strings.TrimSpace(buf.String())
				found = true
			}
			inFence = false
			isJSON = false
			buf.Reset()
			continue
		}
		if isJSON {
			if buf.Len() > 0 {
				buf.WriteByte('\n')
			}
			buf.WriteString(line)
		}
	}
	return lastBlock, found
}

func cleanScoreWhy(raw string) string {
	s := redact.Output(strings.TrimSpace(raw))
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != ' ' {
			return ' '
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "")
	}
	return truncateRunes(s, compareMaxWhyRunes)
}

func truncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	count := 0
	for i := range s {
		if count == maxRunes {
			return s[:i]
		}
		count++
	}
	return s
}
