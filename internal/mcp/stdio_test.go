package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// stdout belongs to the JSON-RPC stream and to nothing else.
//
// This is the failure mode with no useful symptom: one stray line — a log, a
// warning, a fmt.Println left in — and the client's parser abandons the
// session with an error that names neither the line nor this program. So the
// test drives a real session over real pipes and reads every byte that comes
// back, rather than trusting that nothing writes there.
func TestNothingButJSONRPCReachesStdout(t *testing.T) {
	url, _ := liveServer(t)

	stdinReader, stdinWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	realStdin, realStdout := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = stdinReader, stdoutWriter
	t.Cleanup(func() { os.Stdin, os.Stdout = realStdin, realStdout })

	diagnostics := &bytes.Buffer{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, Options{Server: url, Stderr: diagnostics})
	}()

	lines := make(chan string, 16)
	go func() {
		scanner := bufio.NewScanner(stdoutReader)
		scanner.Buffer(make([]byte, 0, 64*1024), 4<<20)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		close(lines)
	}()

	send := func(message string) {
		t.Helper()
		if _, err := stdinWriter.Write([]byte(message + "\n")); err != nil {
			t.Fatalf("write to stdin: %v", err)
		}
	}
	send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":` +
		`{"protocolVersion":"2025-06-18","capabilities":{},` +
		`"clientInfo":{"name":"stdio-test","version":"v1"}}}`)
	send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	send(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)

	// Two requests were sent, so two replies are expected; every line seen on
	// the way must still be JSON-RPC.
	var replies []map[string]any
	deadline := time.After(10 * time.Second)
	for len(replies) < 2 {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatalf("stdout closed after %d replies", len(replies))
			}
			if strings.TrimSpace(line) == "" {
				continue
			}
			var message map[string]any
			if err := json.Unmarshal([]byte(line), &message); err != nil {
				t.Fatalf("a non-JSON line reached stdout: %q", line)
			}
			if message["jsonrpc"] != "2.0" {
				t.Fatalf("a line on stdout was not JSON-RPC: %q", line)
			}
			if _, isReply := message["id"]; isReply {
				replies = append(replies, message)
			}
		case <-deadline:
			t.Fatalf("timed out after %d replies", len(replies))
		}
	}

	// The tool list is the second reply; it must actually carry the tools,
	// otherwise this would pass against a server that answered nothing useful.
	encoded, err := json.Marshal(replies[1]["result"])
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"list_projects", "get_ready_tasks", "get_node"} {
		if !strings.Contains(string(encoded), `"`+name+`"`) {
			t.Fatalf("tools/list did not offer %s: %s", name, encoded)
		}
	}

	// And the startup banner went to stderr, where a person can read it without
	// it corrupting the stream.
	if !strings.Contains(diagnostics.String(), "nodevas mcp:") {
		t.Fatalf("the startup banner did not reach stderr: %q", diagnostics.String())
	}

	cancel()
	_ = stdinWriter.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after its context was cancelled")
	}
}
