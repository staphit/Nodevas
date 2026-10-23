package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"nodevas/internal/runnerwire"
)

// Pull requests: the runner pushes a run's branch with the owner's git
// credentials and opens the PR with the owner's own gh login. The hub never
// talks to GitHub; it only records what the runner reports.

// DirRunner runs a program in a directory, with extra environment and stdin.
// gh needs all three; a CommandRunner that is not also a DirRunner cannot run
// gh, so no pull request is opened through it.
type DirRunner interface {
	// RunIn returns the program's stdout. err is non-nil when it could not
	// start or exited non-zero, and then carries its stderr.
	RunIn(ctx context.Context, dir string, env []string, stdin string, name string, args ...string) (string, error)
}

// RunIn runs a real program.
func (SystemCommands) RunIn(ctx context.Context, dir string, env []string, stdin string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 1000 {
			msg = msg[len(msg)-1000:]
		}
		if msg == "" {
			return stdout.String(), err
		}
		return stdout.String(), errors.New(msg)
	}
	return stdout.String(), nil
}

const (
	pushTimeout = 2 * time.Minute
	ghTimeout   = time.Minute
	// maxPRTitle is GitHub's practical limit, well under its hard one.
	maxPRTitle = 200
	// prViewFields are what the runner reads back about a PR.
	prViewFields = "number,state,isDraft,headRefOid,statusCheckRollup"
)

// ghEnv keeps gh from prompting, colouring or checking for updates.
var ghEnv = []string{"GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1", "GH_SPINNER_DISABLED=1", "NO_COLOR=1",
	"CLICOLOR=0", "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never"}

var errGHMissing = errors.New("gh (GitHub CLI) is not installed on the runner; install it and run gh auth login there")

// ghCLI is gh on this machine.
type ghCLI struct {
	run DirRunner
	bin string
}

func (c *Client) gh() (*ghCLI, error) {
	run, ok := c.Commands.(DirRunner)
	if !ok {
		return nil, errGHMissing
	}
	bin, err := c.Commands.LookPath("gh")
	if err != nil {
		return nil, errGHMissing
	}
	switch strings.ToLower(filepath.Ext(bin)) {
	case ".cmd", ".bat":
		// A PR body cannot cross cmd.exe's parsing safely; gh ships as gh.exe.
		return nil, fmt.Errorf("gh on PATH is a batch file (%s); install the gh.exe release", bin)
	}
	return &ghCLI{run: run, bin: bin}, nil
}

func (g *ghCLI) call(ctx context.Context, dir, stdin string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, ghTimeout)
	defer cancel()
	return g.run.RunIn(ctx, dir, ghEnv, stdin, g.bin, args...)
}

// prRequest is one pull request to open.
type prRequest struct {
	Dir, Base, Branch, Title, Body string
	Draft                          bool
	Labels                         []string
}

// prURLPattern is a PR URL as the hub takes it: https, at most 512 bytes.
var prURLPattern = regexp.MustCompile(`https://[^\s"'<>]{1,480}/pull/(\d{1,12})\b`)

// create opens the PR and returns its URL. A label the repository lacks
// fails the whole create; it is retried once without labels (logf says so).
// A PR that already exists for the branch is taken as the answer.
func (g *ghCLI) create(ctx context.Context, req prRequest, logf func(string, ...any)) (string, error) {
	try := func(labels []string) (string, error) {
		args := []string{"pr", "create", "--base", req.Base, "--head", req.Branch, "--title", req.Title, "--body-file", "-"}
		if req.Draft {
			args = append(args, "--draft")
		}
		for _, label := range labels {
			args = append(args, "--label", label)
		}
		out, err := g.call(ctx, req.Dir, req.Body, args...)
		if err != nil {
			if strings.Contains(err.Error(), "already exists") {
				if found := lastPRURL(err.Error() + "\n" + out); found != "" {
					return found, nil
				}
			}
			return "", err
		}
		if found := lastPRURL(out); found != "" {
			return found, nil
		}
		return "", fmt.Errorf("gh pr create printed no pull request URL: %s", truncate(strings.TrimSpace(out), 300))
	}
	found, err := try(req.Labels)
	if err != nil && len(req.Labels) > 0 && strings.Contains(strings.ToLower(err.Error()), "label") {
		logf("gh pr create failed over a label (%s); retrying without labels", firstLine(err.Error()))
		found, err = try(nil)
	}
	if err != nil {
		return "", ghError("gh pr create", err)
	}
	return found, nil
}

func lastPRURL(s string) string {
	all := prURLPattern.FindAllString(s, -1)
	if len(all) == 0 {
		return ""
	}
	return all[len(all)-1]
}

// prNumber is the number at the end of a PR URL.
func prNumber(prURL string) int {
	m := prURLPattern.FindStringSubmatch(prURL)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// ghError puts gh's failure in terms of what to do about it.
func ghError(what string, err error) error {
	msg := strings.TrimSpace(err.Error())
	lower := strings.ToLower(msg)
	if strings.Contains(lower, "gh auth login") || strings.Contains(lower, "not logged") ||
		strings.Contains(lower, "authentication") || strings.Contains(lower, "http 401") {
		return fmt.Errorf("gh is not signed in on the runner; run gh auth login there (%s)", truncate(firstLine(msg), 300))
	}
	return fmt.Errorf("%s: %s", what, truncate(msg, 1000))
}

// ghPRView is the part of `gh pr view --json` the runner reads.
type ghPRView struct {
	Number            int       `json:"number"`
	State             string    `json:"state"`
	IsDraft           bool      `json:"isDraft"`
	HeadRefOid        string    `json:"headRefOid"`
	StatusCheckRollup []ghCheck `json:"statusCheckRollup"`
}

// ghCheck is a check run (status and conclusion) or a commit status (state).
type ghCheck struct {
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	State      string `json:"state"`
}

// view reads a PR's state, draft flag, head and checks.
func (g *ghCLI) view(ctx context.Context, dir, prURL string) (runnerwire.PRInfo, error) {
	out, err := g.call(ctx, dir, "", "pr", "view", prURL, "--json", prViewFields)
	if err != nil {
		return runnerwire.PRInfo{}, ghError("gh pr view", err)
	}
	return parsePRView(prURL, out)
}

func parsePRView(prURL, out string) (runnerwire.PRInfo, error) {
	var v ghPRView
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &v); err != nil {
		return runnerwire.PRInfo{}, fmt.Errorf("gh pr view printed something other than JSON: %w", err)
	}
	info := runnerwire.PRInfo{URL: prURL, Number: v.Number, Draft: v.IsDraft, HeadSHA: v.HeadRefOid,
		CI: ciRollup(v.StatusCheckRollup)}
	if info.Number == 0 {
		info.Number = prNumber(prURL)
	}
	switch strings.ToUpper(v.State) {
	case "MERGED":
		info.State = runnerwire.PRMerged
	case "CLOSED":
		info.State = runnerwire.PRClosed
	default:
		info.State = runnerwire.PROpen
	}
	return info, nil
}

// ciRollup sums a PR's checks: any failure fails it; otherwise anything still
// running keeps it pending; otherwise it passed. No checks at all is CINone.
func ciRollup(checks []ghCheck) string {
	if len(checks) == 0 {
		return runnerwire.CINone
	}
	failed, pending := false, false
	for _, check := range checks {
		switch checkState(check) {
		case runnerwire.CIFailure:
			failed = true
		case runnerwire.CIPending:
			pending = true
		}
	}
	switch {
	case failed:
		return runnerwire.CIFailure
	case pending:
		return runnerwire.CIPending
	}
	return runnerwire.CISuccess
}

func checkState(check ghCheck) string {
	if check.State != "" && check.Status == "" && check.Conclusion == "" {
		// A commit status.
		switch strings.ToUpper(check.State) {
		case "SUCCESS":
			return runnerwire.CISuccess
		case "FAILURE", "ERROR":
			return runnerwire.CIFailure
		}
		return runnerwire.CIPending // PENDING, EXPECTED
	}
	// A check run: not completed is pending, whatever it says.
	if status := strings.ToUpper(check.Status); status != "" && status != "COMPLETED" {
		return runnerwire.CIPending
	}
	switch strings.ToUpper(check.Conclusion) {
	case "FAILURE", "ERROR", "CANCELLED", "TIMED_OUT", "ACTION_REQUIRED", "STARTUP_FAILURE":
		return runnerwire.CIFailure
	case "SUCCESS", "NEUTRAL", "SKIPPED", "STALE":
		return runnerwire.CISuccess
	}
	return runnerwire.CIPending
}

// prInfo is everything a PR body says about its run.
type prInfo struct {
	job        runnerwire.Job
	result     runnerwire.RunResult
	runnerName string
	hub        string
}

// prTitle is the node's title, or the commit subject when it has none.
func prTitle(job runnerwire.Job, message string) string {
	title := oneLine(job.NodeTitle)
	if title == "" {
		title = firstLine(message)
	}
	if runes := []rune(title); len(runes) > maxPRTitle {
		title = string(runes[:maxPRTitle])
	}
	return title
}

// nodeLink is the node on the hub: the web app opens a node from /popout?node=
// (in the project the browser has open; project is passed for when it reads it).
func nodeLink(hub string, job runnerwire.Job) string {
	if hub == "" || job.NodeID == "" {
		return ""
	}
	q := url.Values{}
	q.Set("node", job.NodeID)
	if job.Project != "" {
		q.Set("project", job.Project)
	}
	return strings.TrimRight(hub, "/") + "/popout?" + q.Encode()
}

// prBody is the PR description: the agent's summary, then where it came from.
func prBody(p prInfo) string {
	var b strings.Builder
	if p.result.Status == runnerwire.RunFailed {
		b.WriteString("> **This run did not finish cleanly**")
		if p.result.Error != "" {
			b.WriteString(": " + oneLine(truncateUTF8(p.result.Error, 500)))
		}
		b.WriteString("\n> The changes it made so far are below, as a draft.\n\n")
	}
	if summary := strings.TrimSpace(p.result.Summary); summary != "" {
		b.WriteString(summary)
	} else {
		b.WriteString("_The agent left no summary._")
	}
	b.WriteString("\n\n---\n\n")
	job := p.job
	node := fmt.Sprintf("%s (`%s`)", oneLine(job.NodeTitle), job.NodeID)
	if link := nodeLink(p.hub, job); link != "" {
		node += " · [open in Nodevas](" + link + ")"
	}
	model := job.Model
	if model == "" {
		model = "default"
	}
	fmt.Fprintf(&b, "- **Node:** %s\n", node)
	if job.Project != "" {
		fmt.Fprintf(&b, "- **Project:** %s\n", oneLine(job.Project))
	}
	fmt.Fprintf(&b, "- **Agent:** %s · model `%s`\n", job.Agent, model)
	if p.runnerName != "" {
		fmt.Fprintf(&b, "- **Runner:** %s\n", oneLine(p.runnerName))
	}
	fmt.Fprintf(&b, "- **Run:** `%s`", job.RunID)
	if job.RequestedBy != "" {
		fmt.Fprintf(&b, ", dispatched by %s", oneLine(job.RequestedBy))
	}
	b.WriteString("\n")
	if u := p.result.Usage; u != nil {
		fmt.Fprintf(&b, "- **Usage:** %s\n", usageText(u))
	}
	fmt.Fprintf(&b, "\nNodevas-Node: %s\nNodevas-Run: %s\n", job.NodeID, job.RunID)
	return b.String()
}

func usageText(u *runnerwire.Usage) string {
	parts := []string{fmt.Sprintf("%d input tokens", u.InputTokens), fmt.Sprintf("%d output tokens", u.OutputTokens)}
	if u.CacheReadTokens > 0 {
		parts = append(parts, fmt.Sprintf("%d cache read", u.CacheReadTokens))
	}
	if u.CacheWriteTokens > 0 {
		parts = append(parts, fmt.Sprintf("%d cache write", u.CacheWriteTokens))
	}
	text := strings.Join(parts, " · ")
	if u.CostUSD > 0 {
		text += fmt.Sprintf(" · ≈ $%.2f (reference only: runs use a subscription login)", u.CostUSD)
	}
	return text
}
