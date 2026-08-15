// nodevas is the Nodevas server binary.
//
//	nodevas serve --project <dir> [--port 5666] [--listen 127.0.0.1]
//	              [--hostname name[,name...]] [--behind-proxy]
//	              [--trusted-proxy ip-or-cidr[,ip-or-cidr...]]
//	nodevas user  add|passwd|remove|role|list --project <dir> [--user <name>]
//	              [--password-stdin]
//
// Pass the password on stdin (--password-stdin) rather than with --password:
// a command line is readable by every process on the machine and lands in the
// shell history. --password still works and warns.
//
// Google Drive backup (optional) accepts OAuth credentials from the App UI.
// They are encrypted in the app secrets directory, outside the workspace.
// Environment variables remain available as a server deployment fallback:
//
//	NODEVAS_GOOGLE_CLIENT_ID / NODEVAS_GOOGLE_CLIENT_SECRET  OAuth client
//	NODEVAS_SECRET_KEY  base64 32-byte key encrypting the stored Drive token
//	                    (a machine-local secrets/master.key is generated if unset)
//
// A subcommand per file: serve.go, user.go, visitor.go. This one is the
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
	case "visitor":
		visitor(os.Args[2:])
	case "mcp":
		mcpServe(os.Args[2:])
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
		"                    [--smtp-host h --smtp-port 587 --smtp-from addr --smtp-user u]  (password: NODEVAS_SMTP_PASSWORD)")
	fmt.Fprintln(os.Stderr,
		"       nodevas user add|passwd|pin|pin-clear|remove|role|list --project <dir> [--user <name>] [--role admin|member]")
	fmt.Fprintln(os.Stderr,
		"                    [--email addr]  (required by `pin`; where one-time passcodes are sent)")
	fmt.Fprintln(os.Stderr,
		"                    [--password-stdin]  (--password and --pin are deprecated: they are visible in the process list)")
	fmt.Fprintln(os.Stderr,
		"       nodevas visitor show|on|off --project <dir> [--pin 777] [--passcode CODE]")
	fmt.Fprintln(os.Stderr,
		"                    (one shared read-only credential; takes effect without a restart)")
	fmt.Fprintln(os.Stderr,
		"       nodevas mcp [--project <name>] [--actor <name>] [--agent-role worker|orchestrator] [--server http://127.0.0.1:5666]")
	fmt.Fprintln(os.Stderr,
		"                    (serves the board to an MCP client over stdin/stdout; needs a running `nodevas serve`)")
}
