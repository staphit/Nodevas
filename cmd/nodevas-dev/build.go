package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

// The build order is the mistake this project invites: the frontend is
// embedded into the Go binary by web/embed.go, so building Go first ships a
// placeholder UI and the server only says "frontend build not embedded" on a
// stderr stream nobody reads. Both buttons exist so the order is one click
// each, in the order they are listed on the page.
type taskKind string

const (
	taskFrontend taskKind = "frontend"
	taskBinary   taskKind = "binary"
)

// buildStatus is what the page shows about the last build. It also carries
// whether web/dist/index.html exists at all, which is the cheapest way to
// warn about a binary that was built out of order.
type buildStatus struct {
	Running     string `json:"running"`
	Last        string `json:"last"`
	LastResult  string `json:"lastResult"`
	FrontendSet bool   `json:"frontendBuilt"`
}

// builder runs the two build commands. It writes into the supervisor's log
// ring so there is one place to read output, and it refuses to run two builds
// at once; it deliberately does not touch the child process, so a rebuild
// while the server runs is allowed and simply produces files the running
// server has already embedded. Restart to pick them up.
type builder struct {
	repoRoot string
	logs     *logRing

	mu      sync.Mutex
	running taskKind
	last    string
	result  string
}

func newBuilder(repoRoot string, logs *logRing) *builder {
	return &builder{repoRoot: repoRoot, logs: logs}
}

func (b *builder) Snapshot() buildStatus {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, err := os.Stat(filepath.Join(b.repoRoot, "web", "dist", "index.html"))
	return buildStatus{
		Running:     string(b.running),
		Last:        b.last,
		LastResult:  b.result,
		FrontendSet: err == nil,
	}
}

// Run executes a build and returns when it is done. The page's fetch waits for
// it; a build takes tens of seconds, which is long for a request but shorter
// than the machinery a progress channel would need for a tool this size.
func (b *builder) Run(kind taskKind) error {
	b.mu.Lock()
	if b.running != "" {
		current := b.running
		b.mu.Unlock()
		return fmt.Errorf("a %s build is already running", current)
	}
	b.running = kind
	b.mu.Unlock()

	err := b.run(kind)

	b.mu.Lock()
	b.running = ""
	b.last = string(kind) + " at " + time.Now().Format("15:04:05")
	if err != nil {
		b.result = "failed: " + err.Error()
	} else {
		b.result = "ok"
	}
	b.mu.Unlock()
	return err
}

func (b *builder) run(kind taskKind) error {
	var cmd *exec.Cmd
	switch kind {
	case taskFrontend:
		npm := "npm"
		if runtime.GOOS == "windows" {
			// npm on Windows is a .cmd shim, which exec cannot run by bare name.
			npm = "npm.cmd"
		}
		cmd = exec.Command(npm, "run", "build")
		cmd.Dir = filepath.Join(b.repoRoot, "web")
	case taskBinary:
		name := "nodevas"
		if runtime.GOOS == "windows" {
			name = "nodevas.exe"
		}
		cmd = exec.Command("go", "build", "-o", name, "./cmd/nodevas")
		cmd.Dir = b.repoRoot
	default:
		return errors.New("unknown build")
	}
	b.logs.Add("build", fmt.Sprintf("running %s in %s", cmd.String(), cmd.Dir))

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	var pipes sync.WaitGroup
	pipes.Add(2)
	go b.pump(&pipes, stdout)
	go b.pump(&pipes, stderr)
	pipes.Wait()
	if err := cmd.Wait(); err != nil {
		b.logs.Add("build", string(kind)+" build failed: "+err.Error())
		return err
	}
	b.logs.Add("build", string(kind)+" build finished")
	return nil
}

func (b *builder) pump(done *sync.WaitGroup, reader io.Reader) {
	defer done.Done()
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		b.logs.Add("build", scanner.Text())
	}
}
