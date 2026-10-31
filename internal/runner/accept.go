package runner

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"nodevas/internal/runnerwire"
)

// Offer is a job as the person accepting it sees it: with the repository's
// local name and path, which only the runner knows.
type Offer struct {
	Job  runnerwire.Job
	Repo runnerwire.Repo
	// Shell is why the agent may run any shell command here
	// (Config.ShellAccess); "" when it cannot.
	Shell string
}

// Accepter decides whether this machine takes a job. It is the point of the
// whole design that a person at the runner says yes, not the hub; see
// docs/workstation.md, "Safe defaults".
type Accepter interface {
	// Accept blocks until someone decides or ctx ends. reason explains a no and
	// is shown on the run.
	Accept(ctx context.Context, offer Offer) (ok bool, reason string)
}

// Decline reasons the terminal accepter gives.
const (
	ReasonNoTerminal = "runner needs someone at its terminal to accept (or `nodevas runner autoaccept on`)"
	ReasonTimedOut   = "nobody accepted at the runner"
	ReasonDeclined   = "declined at the runner"
	ReasonInputGone  = "the runner's terminal input closed"
	ReasonCancelled  = "cancelled before anyone accepted"
)

// DefaultAcceptTimeout is how long an offer waits for an answer.
const DefaultAcceptTimeout = 10 * time.Minute

// TerminalAccepter asks at the runner's terminal: the offer, then
// "Accept? [y/N]". One question at a time, however many offers arrive.
type TerminalAccepter struct {
	In  io.Reader
	Out io.Writer
	// Interactive is false when stdin is not a terminal: nobody can answer,
	// so every offer is declined at once instead of waiting out the timeout.
	Interactive bool
	// Timeout is how long one question waits; zero means DefaultAcceptTimeout.
	Timeout time.Duration

	turn  chan struct{} // one-slot semaphore: whose question is on screen
	once  sync.Once
	lines chan string // stdin, a line at a time; closed at EOF
}

func (t *TerminalAccepter) init() {
	t.once.Do(func() {
		t.turn = make(chan struct{}, 1)
		t.lines = make(chan string)
		// One reader for the life of the process: a blocked read on stdin
		// cannot be cancelled, so it must not belong to any one question.
		go func() {
			defer close(t.lines)
			scanner := bufio.NewScanner(t.In)
			for scanner.Scan() {
				t.lines <- scanner.Text()
			}
		}()
	})
}

func (t *TerminalAccepter) Accept(ctx context.Context, offer Offer) (bool, string) {
	if !t.Interactive {
		return false, ReasonNoTerminal
	}
	t.init()
	select {
	case t.turn <- struct{}{}:
	case <-ctx.Done():
		return false, ReasonCancelled
	}
	defer func() { <-t.turn }()

	// Whatever was typed before the question appeared is not an answer to it.
	for drained := false; !drained; {
		select {
		case _, ok := <-t.lines:
			if !ok {
				return false, ReasonInputGone
			}
		default:
			drained = true
		}
	}

	fmt.Fprint(t.Out, describeOffer(offer))
	fmt.Fprint(t.Out, "Accept? [y/N]: ")
	timeout := t.Timeout
	if timeout <= 0 {
		timeout = DefaultAcceptTimeout
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case line, ok := <-t.lines:
		if !ok {
			fmt.Fprintln(t.Out)
			return false, ReasonInputGone
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "y", "yes":
			return true, ""
		}
		return false, ReasonDeclined
	case <-timer.C:
		fmt.Fprintf(t.Out, "\nno answer in %s; declined.\n", timeout.Round(time.Second))
		return false, ReasonTimedOut
	case <-ctx.Done():
		fmt.Fprintln(t.Out, "\nthe hub cancelled this run; never mind.")
		return false, ReasonCancelled
	}
}

// RemoteTypingAsker is an Accepter that can also ask, once an interactive
// offer is accepted, whether the dispatcher may type into its terminal.
type RemoteTypingAsker interface {
	AllowRemoteTyping(ctx context.Context, offer Offer) bool
}

// AllowRemoteTyping asks "Allow remote typing? [y/N]". Anything but yes, a
// timeout included, leaves the terminal view only.
func (t *TerminalAccepter) AllowRemoteTyping(ctx context.Context, offer Offer) bool {
	if !t.Interactive {
		return false
	}
	t.init()
	select {
	case t.turn <- struct{}{}:
	case <-ctx.Done():
		return false
	}
	defer func() { <-t.turn }()
	fmt.Fprint(t.Out, "The dispatcher can watch this run's terminal. Typing into it answers the agent's\n"+
		"prompts as you (approving commands, edits). Allow remote typing? [y/N]: ")
	timeout := t.Timeout
	if timeout <= 0 {
		timeout = DefaultAcceptTimeout
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case line, ok := <-t.lines:
		if !ok {
			fmt.Fprintln(t.Out)
			return false
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "y", "yes":
			return true
		}
	case <-timer.C:
		fmt.Fprintln(t.Out, "\nno answer; view only.")
	case <-ctx.Done():
	}
	return false
}

// promptPreview is how much of the task the question shows.
const promptPreview = 500

func describeOffer(o Offer) string {
	var b strings.Builder
	job := o.Job
	interactive := job.Mode == runnerwire.ModeInteractive
	if interactive {
		b.WriteString("\n=== INTERACTIVE run offered by the hub ===\n")
	} else {
		b.WriteString("\n=== Run offered by the hub ===\n")
	}
	fmt.Fprintf(&b, "  requested by: %s\n", orDash(job.RequestedBy))
	fmt.Fprintf(&b, "  project:      %s\n", orDash(job.Project))
	fmt.Fprintf(&b, "  node:         %s  %s\n", job.NodeID, job.NodeTitle)
	fmt.Fprintf(&b, "  repository:   %s  (%s)\n", o.Repo.Name, o.Repo.Path)
	if job.Kind == runnerwire.JobVerify {
		b.WriteString("  agent:        none: runs this repository's test command (runner.json), no model\n")
	} else {
		fmt.Fprintf(&b, "  agent:        %s, model %s\n", job.Agent, orDash(job.Model))
	}
	if job.Role != "" {
		fmt.Fprintf(&b, "  role:         %s\n", job.Role)
	}
	if interactive {
		b.WriteString("  mode:         INTERACTIVE: the agent runs in a terminal on this machine; the\n" +
			"                dispatcher watches it, and types into it only if you allow that next\n")
	}
	if o.Shell != "" {
		fmt.Fprintf(&b, "  shell:        the agent may run ANY shell command here (%s)\n", o.Shell)
	}
	switch job.Kind {
	case runnerwire.JobReview:
		fmt.Fprintf(&b, "  kind:         REVIEW of %s (branch %s); posts a PR comment, changes nothing\n", job.PRURL, job.Branch)
	case runnerwire.JobRevise:
		fmt.Fprintf(&b, "  kind:         REVISE %s: pushes to its branch %s\n", job.PRURL, job.Branch)
	case runnerwire.JobVerify:
		phase, head := "", ""
		if job.Verify != nil {
			phase, head = job.Verify.Phase, job.Verify.Head
		}
		fmt.Fprintf(&b, "  kind:         VERIFY (%s) %s at %s: runs the test command on code an agent wrote, in a throwaway worktree\n",
			phase, job.PRURL, short(head))
	case runnerwire.JobJudge:
		fmt.Fprintf(&b, "  kind:         JUDGE: checks %d acceptance criteria; reads, changes no files\n", len(job.Acceptance))
	case runnerwire.JobTest:
		b.WriteString("  kind:         TEST: writes failing tests only, opens a draft pull request\n")
	case runnerwire.JobPlan:
		if strings.TrimSpace(job.Review) != "" {
			fmt.Fprintf(&b, "  kind:         REPLAN: reconciles the goal's tasks with a revised requirement; reads the code, changes no files\n")
		} else {
			fmt.Fprintf(&b, "  kind:         PLAN: breaks the node into tasks on the board; reads the code, changes no files\n")
		}
	default:
		base := job.Base
		if base == "" {
			base = "(the repository's default branch)"
		}
		fmt.Fprintf(&b, "  base:         %s\n", base)
	}
	fmt.Fprintf(&b, "  run:          %s\n", job.RunID)
	prompt := strings.TrimSpace(job.Prompt)
	if utf8.RuneCountInString(prompt) > promptPreview {
		prompt = string([]rune(prompt)[:promptPreview]) + " …"
	}
	b.WriteString("  task:\n")
	for _, line := range strings.Split(prompt, "\n") {
		b.WriteString("    " + line + "\n")
	}
	if review := strings.TrimSpace(job.Review); review != "" &&
		(job.Kind == runnerwire.JobRevise || job.Kind == runnerwire.JobPlan) {
		if utf8.RuneCountInString(review) > promptPreview {
			review = string([]rune(review)[:promptPreview]) + " …"
		}
		if job.Kind == runnerwire.JobPlan {
			b.WriteString("  revision:\n")
		} else {
			b.WriteString("  review to address:\n")
		}
		for _, line := range strings.Split(review, "\n") {
			b.WriteString("    " + line + "\n")
		}
	}
	return printable(b.String())
}

// printable drops control characters but newlines and tabs: the offer comes
// from the hub, and an escape sequence in a node title could otherwise redraw
// the question a person is about to answer.
func printable(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return -1
		}
		return r
	}, s)
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}
