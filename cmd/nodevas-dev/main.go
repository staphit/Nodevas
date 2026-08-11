// nodevas-dev is a local development launcher for the Nodevas server. It
// listens on loopback, opens a browser at itself, and serves one page with
// Start / Stop / Restart buttons around a supervised `nodevas serve` child.
//
// It exists because the two mistakes this project invites -- leaving a server
// running on the port you are about to use, and leaving a second instance
// holding the workspace -- both surface as a child process that dies with a
// message on stderr that nobody reads. The launcher checks for both before it
// spawns anything and says plainly what is in the way.
//
// This is a development tool, and only that. It starts and stops processes
// with the operator's privileges and has no authentication, which is why it
// refuses to bind anything but loopback. It is not a process supervisor for
// production: a deployed Nodevas is `nodevas serve` under the machine's own
// service manager (a systemd unit or equivalent), with TLS or a reverse proxy
// and accounts, none of which this launcher configures or checks. It is a
// clickable sibling of scripts/dev.ps1 and scripts/dev.sh.
//
//	nodevas-dev [--port 5667] [--listen 127.0.0.1] [--project <dir>]
//	            [--server-port 5666] [--binary ./nodevas] [--repo .]
//	            [--open=false]
package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

//go:embed page.html
var pageFS embed.FS

func main() {
	listen := flag.String("listen", "127.0.0.1",
		"address for the control panel; loopback only, this endpoint starts and stops processes")
	port := flag.Int("port", 5667, "port for the control panel")
	projectDir := flag.String("project", ".", "workspace directory passed to `nodevas serve --project`")
	serverPort := flag.Int("server-port", 5666, "port passed to `nodevas serve --port`")
	binary := flag.String("binary", "", "path to the nodevas binary (default ./nodevas, or nodevas.exe on Windows)")
	repo := flag.String("repo", ".", "repository root, used by the rebuild buttons")
	open := flag.Bool("open", true, "open the control panel in a browser at startup")
	flag.Parse()

	// The panel takes no credentials and its POST endpoints spawn and kill
	// processes. Binding it anywhere a second machine can reach is a remote
	// shell, so it is refused rather than warned about.
	if err := requireLoopback(*listen); err != nil {
		log.Fatalf("refusing to start: %v", err)
	}
	if *port < 1 || *port > 65535 {
		log.Fatalf("--port must be between 1 and 65535")
	}

	workspace, err := filepath.Abs(*projectDir)
	if err != nil {
		log.Fatalf("--project: %v", err)
	}
	repoRoot, err := filepath.Abs(*repo)
	if err != nil {
		log.Fatalf("--repo: %v", err)
	}
	binPath, err := resolveBinary(*binary, repoRoot)
	if err != nil {
		log.Fatalf("nodevas binary: %v", err)
	}

	sup := newSupervisor(supervisorConfig{
		Binary:    binPath,
		Workspace: workspace,
		// The child is always loopback-only too: a networked Nodevas needs
		// accounts and TLS, which is a deployment question and not a dev-loop
		// one. Run `nodevas serve` directly for that.
		ServerHost: "127.0.0.1",
		ServerPort: *serverPort,
	})
	builder := newBuilder(repoRoot, sup.logs)

	panelAddr := net.JoinHostPort(*listen, fmt.Sprint(*port))
	panelURL := "http://" + panelAddr + "/"
	mux := http.NewServeMux()
	registerRoutes(mux, sup, builder)

	httpServer := &http.Server{
		Addr:              panelAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	listener, err := net.Listen("tcp", panelAddr)
	if err != nil {
		log.Fatalf("control panel: %v (another nodevas-dev may already be running)", err)
	}

	log.Printf("nodevas-dev control panel on %s", panelURL)
	log.Printf("workspace %s, server port %d, binary %s", workspace, *serverPort, binPath)
	if *open {
		openBrowser(panelURL)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("control panel: %v", err)
		}
	}()

	<-ctx.Done()
	log.Print("shutting down; stopping the server if it is running")
	// The child is ours: leaving it behind is exactly the orphan that causes
	// the port and workspace conflicts this tool exists to explain.
	if err := sup.Stop(); err != nil {
		log.Printf("stop: %v", err)
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownCtx)
}

// requireLoopback rejects any bind address that is not this machine.
func requireLoopback(host string) error {
	trimmed := strings.Trim(strings.TrimSpace(host), "[]")
	if trimmed == "" {
		return errors.New("empty --listen means every interface; use 127.0.0.1")
	}
	if strings.EqualFold(trimmed, "localhost") {
		return nil
	}
	ip := net.ParseIP(trimmed)
	if ip == nil {
		return fmt.Errorf("--listen %q is not an IP address; use 127.0.0.1", host)
	}
	if !ip.IsLoopback() {
		return fmt.Errorf("--listen %q is not loopback; this control panel starts and stops processes and must not be reachable from the network", host)
	}
	return nil
}

// resolveBinary finds the nodevas binary to supervise: an explicit --binary,
// then the repository root (where the README's `go build -o nodevas` puts it),
// then PATH.
func resolveBinary(explicit, repoRoot string) (string, error) {
	if strings.TrimSpace(explicit) != "" {
		path, err := filepath.Abs(explicit)
		if err != nil {
			return "", err
		}
		if _, err := os.Stat(path); err != nil {
			return "", err
		}
		return path, nil
	}
	name := "nodevas"
	if runtime.GOOS == "windows" {
		name = "nodevas.exe"
	}
	candidate := filepath.Join(repoRoot, name)
	if _, err := os.Stat(candidate); err == nil {
		return candidate, nil
	}
	found, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("no %s in %s and none on PATH; run `go build -o %s ./cmd/nodevas` (after `cd web && npm ci && npm run build`)",
			name, repoRoot, name)
	}
	return found, nil
}

// openBrowser is best effort: a launcher that fails to start because a browser
// could not be opened would be worse than one that prints the URL.
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		log.Printf("could not open a browser (%v); go to %s", err, url)
		return
	}
	go func() { _ = cmd.Wait() }()
}

func registerRoutes(mux *http.ServeMux, sup *supervisor, builds *builder) {
	page, err := pageFS.ReadFile("page.html")
	if err != nil {
		log.Fatalf("embedded page: %v", err)
	}
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(page)
	})
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, sup.Snapshot(builds.Snapshot()))
	})
	mux.HandleFunc("POST /api/start", guard(func(w http.ResponseWriter, r *http.Request) {
		respond(w, sup, builds, sup.Start())
	}))
	mux.HandleFunc("POST /api/stop", guard(func(w http.ResponseWriter, r *http.Request) {
		respond(w, sup, builds, sup.Stop())
	}))
	mux.HandleFunc("POST /api/restart", guard(func(w http.ResponseWriter, r *http.Request) {
		respond(w, sup, builds, sup.Restart())
	}))
	// Killing the process on the port is a separate, explicitly requested
	// action, and it only accepts the pid this launcher itself reported. A dev
	// tool that decides on its own to kill something will one day kill the
	// wrong thing.
	mux.HandleFunc("POST /api/release-port", guard(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			PID int `json:"pid"`
		}
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body)
		respond(w, sup, builds, sup.ReleasePort(body.PID))
	}))
	mux.HandleFunc("POST /api/build-frontend", guard(func(w http.ResponseWriter, r *http.Request) {
		respond(w, sup, builds, builds.Run(taskFrontend))
	}))
	mux.HandleFunc("POST /api/build-binary", guard(func(w http.ResponseWriter, r *http.Request) {
		respond(w, sup, builds, builds.Run(taskBinary))
	}))
}

// guard keeps a web page on some other origin from driving these endpoints.
// They spawn and kill processes, so a cross-site POST is a real capability;
// requiring a header no simple form can set forces a preflight the browser
// will refuse, and the Origin check covers anything that gets past that.
func guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Nodevas-Dev") != "1" {
			http.Error(w, "missing X-Nodevas-Dev header", http.StatusForbidden)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && !isLocalOrigin(origin) {
			http.Error(w, "cross-origin request refused", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

func isLocalOrigin(origin string) bool {
	trimmed := strings.TrimPrefix(strings.TrimPrefix(origin, "http://"), "https://")
	host, _, err := net.SplitHostPort(trimmed)
	if err != nil {
		host = trimmed
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

// respond answers a control action with the same status document the page
// polls, so a click updates everything the operator can see in one round trip.
func respond(w http.ResponseWriter, sup *supervisor, b *builder, err error) {
	snapshot := sup.Snapshot(b.Snapshot())
	if err != nil {
		snapshot.ActionError = err.Error()
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
