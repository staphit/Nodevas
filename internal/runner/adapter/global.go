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

// GlobalPrompt is a global agent's headless preamble, then its task.
func GlobalPrompt(job runnerwire.Job, branch string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are running headless for Nodevas as a global agent (no board, no project), in a dedicated "+
		"git worktree on branch %s. Nobody can answer questions during this run: decide sensibly and state your "+
		"assumptions in your summary.\n\n", branch)
	b.WriteString("Rules for this run:\n")
	b.WriteString("1. Work only inside this worktree (the current directory). Do not touch other checkouts.\n")
	b.WriteString("2. Do not push, and do not open pull requests: " + globalPublishText(job, "you finish") + ".\n")
	b.WriteString("3. Finish with a short summary of what you changed.\n\n")
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
	b.WriteString("The task:\n\n")
	b.WriteString(job.Prompt)
	b.WriteString("\n")
	return b.String()
}

// globalPublishText says what happens to the work: kept on the branch when
// nobody asked for a pull request, else the runner's to publish after when.
func globalPublishText(job runnerwire.Job, when string) string {
	if job.NoPR {
		return "the runner keeps your branch for its owner to review"
	}
	return "the runner handles that after " + when
}
