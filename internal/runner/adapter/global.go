package adapter

import (
	"fmt"
	"strings"

	"nodevas/internal/runnerwire"
)

// Global agents (agent workspace, phase 2; runnerwire/global.go): a run the
// runner's owner started from the hub in one of the runner's repositories,
// for no project and no node. It has no board (RunEnv.NoBoard), so its
// preamble names no MCP tool: nothing to claim, no status to set and, run in
// the background, nobody to ask.

// GlobalPrompt is a global agent's headless preamble, then its task (or
// carry-over section when continuing earlier work without session resume).
func GlobalPrompt(job runnerwire.Job, branch string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are running headless for Nodevas as a global agent (no board, no project), in a dedicated "+
		"git worktree on branch %s. Nobody can answer during this run: decide sensibly. If you need a decision, "+
		"ask it as the last line of your final message; the person's reply continues this work.\n\n", branch)
	b.WriteString("Rules for this run:\n")
	b.WriteString("1. Work only inside this worktree (the current directory). Do not touch other checkouts.\n")
	b.WriteString("2. Do not push, and do not open pull requests: " + globalPublishText(job, "you finish") + ".\n")
	b.WriteString("3. Finish with a short summary of what you changed.\n\n")
	if job.ContinueOf != "" {
		b.WriteString(ContinueSection(continueInputFromJob(job, branch)))
		return b.String()
	}
	b.WriteString("The task:\n\n")
	b.WriteString(job.Prompt)
	b.WriteString("\n")
	return b.String()
}

// GlobalInteractivePrompt is a global agent's interactive preamble: the
// person who started it may be at the terminal.
func GlobalInteractivePrompt(job runnerwire.Job, branch string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are running interactively for Nodevas as a global agent (no board, no project), in a "+
		"dedicated git worktree on branch %s. The person who started you may be watching this terminal and may "+
		"type to you.\n\n", branch)
	b.WriteString("Rules for this run:\n")
	b.WriteString("1. Work only inside this worktree (the current directory). Do not touch other checkouts.\n")
	b.WriteString("2. Do not push, and do not open pull requests: " + globalPublishText(job, "the session ends") + ".\n")
	b.WriteString("3. When you are done, say so; the session ends when the person quits it.\n\n")
	if job.ContinueOf != "" {
		b.WriteString(ContinueSection(continueInputFromJob(job, branch)))
		return b.String()
	}
	b.WriteString("The task:\n\n")
	b.WriteString(job.Prompt)
	b.WriteString("\n")
	return b.String()
}

// globalPublishText says what happens to the work: kept on the branch when
// nobody asked for a pull request, else the runner's to publish after when.
func globalPublishText(job runnerwire.Job, when string) string {
	if pr := strings.TrimSpace(job.ContinuePR); pr != "" {
		return "the runner pushes your commits to pull request " + pr + " after " + when
	}
	if job.NoPR {
		return "the runner keeps your branch for its owner to review"
	}
	return "the runner handles that after " + when
}
