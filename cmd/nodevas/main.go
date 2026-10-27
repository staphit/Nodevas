// nodevas is the Nodevas server binary.
//
//	nodevas serve --project <dir> [--port 5666] [--listen 127.0.0.1]
//	              [--hostname name[,name...]] [--behind-proxy]
//	              [--trusted-proxy ip-or-cidr[,ip-or-cidr...]]
//	              [--repo <git dir>]... [--no-runner | --with-runner]
//	nodevas user  add|passwd|email|remove|role|list --project <dir> [--user <name>]
//	              [--email addr | --clear] [--password-stdin]
//	nodevas account totp-reset [--project <dir>] <email-or-id-or-name>
//
// Web sign-in is an email address and a passcode mailed to it, so
// `nodevas user email --user X --email Y` is what lets somebody in, and
// `--clear` is what stops them.
//
// Pass the password on stdin (--password-stdin) rather than with --password:
// a command line is readable by every process on the machine and lands in the
// shell history. --password still works and warns.
//
// Secrets are encrypted in the app secrets directory, outside the workspace.
// Environment variables remain available as a server deployment fallback:
//
//	NODEVAS_SMTP_PASSWORD / NODEVAS_SMTP_PASSWORD_FILE  SMTP secret
//	NODEVAS_SECRET_KEY  base64 32-byte key encrypting stored secrets
//	                    (a machine-local secrets/master.key is generated if unset)
//	NODEVAS_DATABASE_URL  postgres://… to keep accounts, sessions, settings and
//	                    the audit trail on a PostgreSQL server instead of the
//	                    workspace's SQLite file (the search index stays local)
//
// A subcommand per file: serve.go, user.go, account.go, db.go, runner.go, agents.go. This one is the
// dispatch and the usage text, so what the binary accepts can be read without
// reading what any of it does.
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "serve":
		serve(os.Args[2:])
	case "user":
		user(os.Args[2:])
	case "mcp":
		mcpServe(os.Args[2:])
	case "db":
		database(os.Args[2:])
	case "hook":
		hookCmd(os.Args[2:])
	case "setup-agent":
		setupAgentCmd(os.Args[2:])
	case "runner":
		os.Exit(runnerCmd(os.Args[2:], os.Stdin, os.Stdout, os.Stderr))
	case "agents":
		os.Exit(agentsCmd(os.Args[2:], os.Stdout, os.Stderr))
	case "account":
		os.Exit(accountCmd(os.Args[2:], os.Stdout, os.Stderr))
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr,
		"usage: nodevas serve --project <dir> [--config nodevas.yaml] [--port 5666] [--listen 127.0.0.1]")
	fmt.Fprintln(os.Stderr,
		"                    [--hostname name[,name...]] [--behind-proxy]")
	fmt.Fprintln(os.Stderr,
		"                    [--trusted-proxy ip-or-cidr[,ip-or-cidr...]]")
	fmt.Fprintln(os.Stderr,
		"                    [--max-active-users N]")
	fmt.Fprintln(os.Stderr,
		"                    [--repo <git dir>]... [--no-runner | --with-runner]  (loopback: a runner here starts by default)")
	fmt.Fprintln(os.Stderr,
		"                    [--smtp-host h --smtp-port 587 --smtp-from addr --smtp-user u]  (password: NODEVAS_SMTP_PASSWORD or NODEVAS_SMTP_PASSWORD_FILE)")
	fmt.Fprintln(os.Stderr,
		"       nodevas user add|passwd|email|remove|role|list --project <dir> [--user <name>] [--role admin|member]")
	fmt.Fprintln(os.Stderr,
		"                    [--email addr]  (the web sign-in address; passcodes are mailed there. Required by `email`, optional for `add`)")
	fmt.Fprintln(os.Stderr,
		"                    [--clear]  (with `email`: remove web sign-in for the account)")
	fmt.Fprintln(os.Stderr,
		"                    [--password-stdin]  (--password is deprecated: it is visible in the process list)")
	fmt.Fprintln(os.Stderr,
		"       nodevas account totp-reset [--project <dir>] <email-or-id-or-name>  (turns two-factor sign-in off for a locked-out account)")
	fmt.Fprintln(os.Stderr,
		"       nodevas mcp [--project <name>] [--actor <name>] [--agent-role worker|orchestrator] [--toolset full|run] [--model m] [--server http://127.0.0.1:5666]")
	fmt.Fprintln(os.Stderr,
		"                    (serves the board to an MCP client over stdin/stdout; needs a running `nodevas serve`)")
	fmt.Fprintln(os.Stderr,
		"       nodevas db status|copy-to-postgres --project <dir> [--replace]")
	fmt.Fprintln(os.Stderr,
		"       nodevas setup-agent [--agents claude,codex,gemini,opencode,antigravity] [--remove] [--dry-run] [--server url] [--project <name>] [--actor <name>]")
	fmt.Fprintln(os.Stderr,
		"       nodevas hook <claude|codex|gemini|opencode|antigravity> <session-start|user-prompt-submit|post-tool-use|stop|session-end> [--server url] [--project <name>] [--actor <name>]")
	fmt.Fprintln(os.Stderr,
		"       nodevas runner [start|login|logout|status|repo]  (connects this machine to a hub; nodevas runner help)")
	fmt.Fprintln(os.Stderr,
		"       nodevas agents who|send|listen|exclude  (the agent network from a terminal; nodevas agents help)")
	fmt.Fprintln(os.Stderr,
		"                    (PostgreSQL: NODEVAS_DATABASE_URL; unreachable within NODEVAS_DATABASE_WAIT=30s falls back to SQLite)")
}
