package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"

	"nodevas/internal/runnerwire"
)

// helloDeadline is how long the fake hub of these tests waits for a hello:
// far shorter than any agent CLI is given to answer.
const helloDeadline = 300 * time.Millisecond

// openCodeCommands is a machine with OpenCode installed, listing one model.
func openCodeCommands() *fakeCommands {
	return &fakeCommands{
		paths: map[string]string{"opencode": "/bin/opencode"},
		outputs: map[string]string{
			"/bin/opencode --version": "1.18.23\n",
			"/bin/opencode models":    "opencode/big-pickle\n",
		},
	}
}

func agentOf(t *testing.T, agents []runnerwire.Agent, kind string) runnerwire.Agent {
	t.Helper()
	for _, agent := range agents {
		if agent.Kind == kind {
			return agent
		}
	}
	t.Fatalf("no %s in %+v", kind, agents)
	return runnerwire.Agent{}
}

func modelIDs(models []runnerwire.Model) []string {
	var ids []string
	for _, m := range models {
		ids = append(ids, m.ID)
	}
	return ids
}

// nextHello is the next hello the hub hears, within wait.
func nextHello(t *testing.T, hub *fakeHub, wait time.Duration, what string) runnerwire.Info {
	t.Helper()
	select {
	case info := <-hub.hellos:
		return info
	case <-time.After(wait):
		t.Fatalf("%s: no hello within %s (the hub gave up on %d connections that never said hello)", what, wait, hub.late.Load())
	}
	return runnerwire.Info{}
}

// startClient runs client until the test ends.
func startClient(t *testing.T, client *Client) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- client.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("runner did not stop")
		}
	})
}

// The hub closes a connection that does not say hello within seconds, and
// an agent CLI can take longer than that to list its models. A reconnect
// says hello at once with what the runner already knows; it never asks the
// CLIs first. (It used to: the hub gave up before the hello, every time, and
// the runner never came online.)
func TestReconnectSaysHelloWithoutAskingTheAgentsAgain(t *testing.T) {
	hub, srv := newFakeHub(t)
	hub.helloWithin.Store(int64(helloDeadline))
	client := testClient(srv)
	cmds := openCodeCommands()
	client.Commands = cmds
	client.RedetectInterval = 20 * time.Millisecond
	startClient(t, client)

	first := nextHello(t, hub, 5*time.Second, "first connection")
	want := []string{"default", "opencode/big-pickle"}
	if oc := agentOf(t, first.Agents, "opencode"); !oc.Installed || !slices.Equal(modelIDs(oc.Models), want) {
		t.Fatalf("opencode in the first hello = %+v", oc)
	}

	// From here on OpenCode never answers when asked for its models, and the
	// hub restarts while a look waits for it.
	cmds.hang("/bin/opencode models")
	eventually(t, "a look hanging on opencode's models", func() bool { return cmds.hanging("/bin/opencode models") == 1 })
	hub.drop()
	second := nextHello(t, hub, 3*time.Second, "after the hub dropped the connection")
	if oc := agentOf(t, second.Agents, "opencode"); !oc.Installed || !slices.Equal(modelIDs(oc.Models), want) {
		t.Errorf("opencode after the reconnect = %+v", oc)
	}
	if late := hub.late.Load(); late != 0 {
		t.Errorf("the hub gave up on %d connections that never said hello", late)
	}
}

// The runner connects only once its first look at this machine is over, so
// the first hello already lists every model: the hub offers queued runs
// right after it, and a run declined for a model not listed yet stays
// declined.
func TestFirstHelloWaitsForTheFirstLook(t *testing.T) {
	hub, srv := newFakeHub(t)
	hub.helloWithin.Store(int64(helloDeadline))
	client := testClient(srv)
	cmds := openCodeCommands()
	client.Commands = cmds
	release := cmds.hang("/bin/opencode models")
	startClient(t, client)

	eventually(t, "opencode asked for its models", func() bool { return cmds.ran("/bin/opencode models") == 1 })
	time.Sleep(2 * helloDeadline)
	if n := hub.conns.Load(); n != 0 {
		t.Fatalf("the runner connected %d times before it knew its agents", n)
	}
	release()
	first := nextHello(t, hub, 5*time.Second, "after the first look")
	if oc := agentOf(t, first.Agents, "opencode"); !oc.Installed ||
		!slices.Equal(modelIDs(oc.Models), []string{"default", "opencode/big-pickle"}) {
		t.Errorf("opencode in the first hello = %+v", oc)
	}
	if late := hub.late.Load(); late != 0 {
		t.Errorf("the hub gave up on %d connections that never said hello", late)
	}
}

// A CLI that never lists its models holds the first look for its timeout and
// no longer: the runner then comes online with the built-in list, and its
// log names the command and how long the look took.
func TestFirstLookIsCutAtTheCatalogTimeout(t *testing.T) {
	hub, srv := newFakeHub(t)
	hub.helloWithin.Store(int64(helloDeadline))
	client := testClient(srv)
	cmds := openCodeCommands()
	cmds.paths["claude"] = "/bin/claude"
	cmds.outputs["/bin/claude --version"] = "2.1.0 (Claude Code)\n"
	client.Commands = cmds
	client.catalogTimeout = 50 * time.Millisecond
	var log syncBuffer
	client.Log = &log
	cmds.hang("/bin/opencode models")
	startClient(t, client)

	first := nextHello(t, hub, 5*time.Second, "first connection")
	if oc := agentOf(t, first.Agents, "opencode"); !oc.Installed || oc.Error != "" ||
		!slices.Equal(modelIDs(oc.Models), []string{"default"}) {
		t.Errorf("opencode = %+v", oc)
	}
	if claude := agentOf(t, first.Agents, "claude"); !claude.Installed || len(claude.Models) != 4 {
		t.Errorf("claude = %+v", claude)
	}
	if late := hub.late.Load(); late != 0 {
		t.Errorf("the hub gave up on %d connections that never said hello", late)
	}
	said := log.String()
	if !strings.Contains(said, "runner: checked agents in ") ||
		!strings.Contains(said, "(opencode models: no answer after 50ms, using the built-in model list)") {
		t.Errorf("log = %q", said)
	}
}

// Looking again happens off the connection: while a CLI hangs on its model
// list, pings go out as usual, nothing new is said, and the hub is given no
// reason to drop the runner.
func TestPingsFlowWhileALookHangs(t *testing.T) {
	hub, srv := newFakeHub(t)
	hub.helloWithin.Store(int64(helloDeadline))
	client := testClient(srv)
	cmds := openCodeCommands()
	client.Commands = cmds
	client.RedetectInterval = 20 * time.Millisecond
	startClient(t, client)
	nextHello(t, hub, 5*time.Second, "first connection")

	release := cmds.hang("/bin/opencode models")
	eventually(t, "a look hanging on opencode's models", func() bool { return cmds.hanging("/bin/opencode models") == 1 })
	before := hub.pings.Load()
	eventually(t, "five more pings while the look hangs", func() bool { return hub.pings.Load() >= before+5 })
	if n := hub.conns.Load(); n != 1 {
		t.Errorf("connections = %d, want the first one still", n)
	}

	// The look ends having found what the last one found: no hello.
	looks := cmds.ran("/bin/opencode --version")
	release()
	eventually(t, "three more looks", func() bool { return cmds.ran("/bin/opencode --version") >= looks+3 })
	select {
	case info := <-hub.hellos:
		t.Errorf("a look that found nothing new said hello again: %+v", info.Agents)
	default:
	}
}

// A look that finds something new says so once: one hello, and none for the
// looks after it that find the same.
func TestChangedLookSaysHelloOnce(t *testing.T) {
	hub, srv := newFakeHub(t)
	client := testClient(srv)
	cmds := openCodeCommands()
	client.Commands = cmds
	client.RedetectInterval = 20 * time.Millisecond
	var log syncBuffer
	client.Log = &log
	startClient(t, client)
	nextHello(t, hub, 5*time.Second, "first connection")

	cmds.set("/bin/opencode models", "opencode/big-pickle\nopencode/small-pickle\n")
	second := nextHello(t, hub, 5*time.Second, "after OpenCode listed another model")
	if oc := agentOf(t, second.Agents, "opencode"); !slices.Equal(modelIDs(oc.Models),
		[]string{"default", "opencode/big-pickle", "opencode/small-pickle"}) {
		t.Errorf("opencode = %+v", oc)
	}
	looks := cmds.ran("/bin/opencode models")
	eventually(t, "five more looks", func() bool { return cmds.ran("/bin/opencode models") >= looks+5 })
	select {
	case info := <-hub.hellos:
		t.Errorf("a second hello for one change: %+v", info.Agents)
	default:
	}
	// The first look and the one that found the change are in the log; the
	// quick ones that found nothing new are not.
	if said := log.String(); strings.Count(said, "checked agents in ") != 2 ||
		strings.Count(said, "; what was found changed, telling the hub") != 1 {
		t.Errorf("log = %q", said)
	}
}

// "Check again" in the app: the runner looks at its agents anew, says what
// it found in a hello, and only then answers. A look that takes longer than
// the app waits is answered with what is known.
func TestCheckAgainOverTheSocket(t *testing.T) {
	env := startBuiltInRunner(t, func(c *Client) { c.checkAgainWait = 200 * time.Millisecond })
	env.hub.hello()
	cmds := env.client.Commands.(*fakeCommands)
	checkAgain := func(id string) []runnerwire.Envelope {
		t.Helper()
		env.hub.send(runnerwire.Envelope{Type: runnerwire.TypeSettingsRequest,
			Settings: &runnerwire.SettingsRequest{ID: id, AccountID: wsOwner, Redetect: true}})
		var done *runnerwire.SettingsDone
		eventually(t, "settings_done "+id, func() bool {
			for _, msg := range env.hub.messages("") {
				if msg.Type == runnerwire.TypeSettingsDone && msg.SettingsDone.ID == id {
					done = msg.SettingsDone
					return true
				}
			}
			return false
		})
		if done.Code != "" {
			t.Fatalf("settings_done %s = %+v", id, done)
		}
		// The hub read every hello sent before the answer before the answer.
		var hellos []runnerwire.Envelope
		for {
			select {
			case hello := <-env.hub.hellos:
				hellos = append(hellos, hello)
				continue
			default:
			}
			return hellos
		}
	}

	cmds.set("/bin/codex --version", "codex 0.2\n")
	hellos := checkAgain("s-1")
	if len(hellos) == 0 {
		t.Fatal("no hello before the answer")
	}
	if codex := agentOf(t, hellos[len(hellos)-1].Hello.Agents, "codex"); codex.Version != "codex 0.2" {
		t.Errorf("codex after checking again = %+v", codex)
	}

	// Codex stops answering: the answer comes once the wait is over, long
	// before the look gives up on Codex, and says what was known.
	cmds.hang("/bin/codex --version")
	start := time.Now()
	hellos = checkAgain("s-2")
	if took := time.Since(start); took > versionTimeout/2 {
		t.Errorf("the answer waited %s for a look that hangs", took)
	}
	if len(hellos) == 0 {
		t.Fatal("no hello before the answer")
	}
	if codex := agentOf(t, hellos[len(hellos)-1].Hello.Agents, "codex"); !codex.Installed || codex.Version != "codex 0.2" {
		t.Errorf("codex while the look hangs = %+v", codex)
	}
}

// Every CLI is asked for its version at once: one that hangs costs its own
// timeout and hides no other agent.
func TestHungVersionHidesNoOtherAgent(t *testing.T) {
	cmds := &fakeCommands{
		paths: map[string]string{"claude": "/bin/claude", "codex": "/bin/codex", "gemini": "/bin/gemini"},
		outputs: map[string]string{
			"/bin/claude --version": "2.1.0 (Claude Code)\n",
			"/bin/gemini --version": "0.9.1\n",
		},
	}
	cmds.hang("/bin/codex --version")
	p := newProber(cmds)
	p.versionTimeout = 50 * time.Millisecond
	agents, said := p.look(context.Background(), nil)
	if claude, gemini := agentOf(t, agents, "claude"), agentOf(t, agents, "gemini"); !claude.Installed || !gemini.Installed {
		t.Errorf("claude = %+v, gemini = %+v", claude, gemini)
	}
	if codex := agentOf(t, agents, "codex"); codex.Installed || codex.Error != "codex --version: no answer after 50ms" ||
		!slices.Equal(modelIDs(codex.Models), modelIDs(DefaultModels("codex"))) {
		t.Errorf("codex = %+v", codex)
	}
	if !slices.Equal(said.problems, []string{"codex --version: no answer after 50ms"}) || len(said.notes) != 0 {
		t.Errorf("report = %+v", said)
	}

	// At once, not one after the other: both hang, and both were asked
	// before either gave up.
	cmds.hang("/bin/claude --version")
	p.versionTimeout = 10 * time.Second
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		p.versions(ctx, nil)
	}()
	eventually(t, "claude and codex asked while both hang", func() bool {
		return cmds.ran("/bin/claude --version") == 2 && cmds.ran("/bin/codex --version") == 2
	})
	cancel()
	<-done
}

// The installed kinds come from the versions alone: no CLI is asked for its
// models, which is the slow part.
func TestInstalledKindsAsksNoCatalog(t *testing.T) {
	cmds := openCodeCommands()
	cmds.paths["codex"] = "/bin/codex"
	cmds.outputs["/bin/codex --version"] = "codex-cli 0.156.0\n"
	if kinds := InstalledKinds(context.Background(), cmds, nil); !slices.Equal(kinds, []string{"codex", "opencode"}) {
		t.Errorf("installed = %v", kinds)
	}
	if cmds.ran("/bin/opencode models") != 0 || cmds.ran("/bin/codex debug models --bundled") != 0 {
		t.Errorf("a catalog was asked for: %v", cmds.calls)
	}
}

// "Check again" while a look is under way waits for the next one: the look
// already running may have passed the agent the owner just installed.
func TestCheckAgainWaitsForALookThatStartsAfterIt(t *testing.T) {
	cmds := openCodeCommands()
	client := &Client{Config: &Config{}, Commands: cmds, RedetectInterval: 20 * time.Millisecond}
	client.detect = newDetector(client)
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		client.detect.run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-stopped
	})
	<-client.detect.first

	release := cmds.hang("/bin/opencode models")
	eventually(t, "a look hanging on opencode's models", func() bool { return cmds.hanging("/bin/opencode models") == 1 })
	looks := cmds.ran("/bin/opencode --version")
	answered := make(chan struct{})
	go func() {
		defer close(answered)
		client.detect.again(ctx, 10*time.Second)
	}()
	time.Sleep(50 * time.Millisecond)
	release()
	select {
	case <-answered:
	case <-time.After(10 * time.Second):
		t.Fatal("check again never answered")
	}
	if n := cmds.ran("/bin/opencode --version"); n < looks+1 {
		t.Errorf("check again answered with the look that was already under way (%d looks, %d before)", n, looks)
	}
}

// A CLI that fails to list its models keeps the list it gave last time; the
// built-in list is for a CLI that never gave one, or that answered with
// nothing usable.
func TestFailedCatalogKeepsTheLastList(t *testing.T) {
	cmds := openCodeCommands()
	p := newProber(cmds)
	look := func() (runnerwire.Agent, report) {
		t.Helper()
		agents, said := p.look(context.Background(), nil)
		return agentOf(t, agents, "opencode"), said
	}
	listed := []string{"default", "opencode/big-pickle"}
	if oc, said := look(); !slices.Equal(modelIDs(oc.Models), listed) || len(said.problems) != 0 {
		t.Fatalf("first look: %+v, %+v", oc, said)
	}

	cmds.errs = map[string]error{"/bin/opencode models": errors.New("exit status 1: no providers")}
	oc, said := look()
	if !oc.Installed || !slices.Equal(modelIDs(oc.Models), listed) {
		t.Errorf("after a failure: %+v", oc)
	}
	if len(said.problems) != 1 || !strings.HasPrefix(said.problems[0], "opencode models: failed after ") ||
		!strings.HasSuffix(said.problems[0], " (exit status 1: no providers), using the list from the last check") {
		t.Errorf("report = %+v", said)
	}

	// It answers again, with nothing usable: that is its answer.
	cmds.errs = nil
	cmds.outputs["/bin/opencode models"] = "no models configured\n"
	if oc, said := look(); !slices.Equal(modelIDs(oc.Models), []string{"default"}) || len(said.problems) != 0 {
		t.Errorf("after an empty answer: %+v, %+v", oc, said)
	}
	cmds.errs = map[string]error{"/bin/opencode models": errors.New("exit status 1")}
	if oc, said := look(); !slices.Equal(modelIDs(oc.Models), []string{"default"}) || len(said.problems) != 1 ||
		!strings.HasSuffix(said.problems[0], ", using the built-in model list") {
		t.Errorf("a failure with no list to keep: %+v, %+v", oc, said)
	}

	// A list from before the CLI went away is not kept for its return.
	cmds.errs = nil
	cmds.outputs["/bin/opencode models"] = "opencode/big-pickle\n"
	look()
	cmds.errs = map[string]error{"/bin/opencode --version": errors.New("exit status 1")}
	if oc, _ := look(); oc.Installed || !slices.Equal(modelIDs(oc.Models), []string{"default"}) {
		t.Errorf("not usable: %+v", oc)
	}
	cmds.errs = map[string]error{"/bin/opencode models": errors.New("exit status 1")}
	if oc, _ := look(); !oc.Installed || !slices.Equal(modelIDs(oc.Models), []string{"default"}) {
		t.Errorf("back, and failing to list: %+v", oc)
	}
}

// manyModels is a list of n models: "default", then prefix-1, prefix-2, …
func manyModels(prefix string, n int) []runnerwire.Model {
	models := []runnerwire.Model{{ID: "default", Label: "CLI default", Default: true}}
	for i := 1; i < n; i++ {
		models = append(models, runnerwire.Model{ID: fmt.Sprintf("%s-%d", prefix, i)})
	}
	return models
}

// The hub refuses a hello that lists more than runnerwire.MaxModels over
// all its agents, and closes the connection. The longest lists are cut
// until they fit; every agent keeps its default.
func TestTrimModels(t *testing.T) {
	total := func(agents []runnerwire.Agent) int {
		n := 0
		for _, agent := range agents {
			n += len(agent.Models)
		}
		return n
	}
	long, longer := manyModels("agy", 40), manyModels("oc", 64)
	agents := []runnerwire.Agent{
		{Kind: "claude", Models: DefaultModels("claude")},
		{Kind: "codex", Models: DefaultModels("codex")},
		{Kind: "gemini", Models: DefaultModels("gemini")},
		{Kind: "opencode", Models: longer},
		{Kind: "antigravity", Models: long},
	}
	notes := trimModels(agents)
	if total(agents) != runnerwire.MaxModels {
		t.Fatalf("models after the cut = %d, want %d", total(agents), runnerwire.MaxModels)
	}
	// The short lists are whole; the two long ones were cut to the same
	// length, from the end.
	if len(agents[0].Models) != 4 || len(agents[1].Models) != 3 || len(agents[2].Models) != 3 {
		t.Errorf("a short list was cut: %d %d %d", len(agents[0].Models), len(agents[1].Models), len(agents[2].Models))
	}
	for i, whole := range map[int][]runnerwire.Model{3: longer, 4: long} {
		kept := agents[i].Models
		if len(kept) != 27 || !slices.Equal(modelIDs(kept), modelIDs(whole[:27])) {
			t.Errorf("%s keeps %v", agents[i].Kind, modelIDs(kept))
		}
	}
	if len(longer) != 64 || longer[63].ID != "oc-63" || len(long) != 40 {
		t.Error("the lists handed in were changed")
	}
	if !slices.Equal(notes, []string{
		"opencode: offering 27 of its 64 models, all agents together may list 64",
		"antigravity: offering 27 of its 40 models, all agents together may list 64",
	}) {
		t.Errorf("notes = %q", notes)
	}

	// Lists that fit are left alone.
	fits := []runnerwire.Agent{{Kind: "claude", Models: DefaultModels("claude")}, {Kind: "opencode", Models: manyModels("oc", 60)}}
	if notes := trimModels(fits); notes != nil || total(fits) != 64 {
		t.Errorf("lists that fit: %d models, notes %q", total(fits), notes)
	}

	// The owner's own list may put its default anywhere: it stays.
	own := manyModels("mine", 80)[1:]
	own[len(own)-1].Default = true
	owned := []runnerwire.Agent{{Kind: "codex", Models: own}, {Kind: "claude", Models: DefaultModels("claude")}}
	trimModels(owned)
	if kept := owned[0].Models; total(owned) != runnerwire.MaxModels || kept[len(kept)-1].ID != "mine-79" || kept[0].ID != "mine-1" {
		t.Errorf("own list = %v", modelIDs(owned[0].Models))
	}
}

// A look never reports more models than a hello may carry.
func TestDetectFitsItsModelsInOneHello(t *testing.T) {
	var listed strings.Builder
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&listed, "provider/model-%d\n", i)
	}
	cmds := openCodeCommands()
	cmds.outputs["/bin/opencode models"] = listed.String()
	info := BuildInfo(context.Background(), cmds, &Config{})
	models := 0
	for _, agent := range info.Agents {
		models += len(agent.Models)
		if len(agent.Models) == 0 || agent.Models[0].ID != "default" {
			t.Errorf("%s lost its default: %v", agent.Kind, modelIDs(agent.Models))
		}
	}
	// Claude 4, Codex 3, Gemini 3 and agy 1 leave OpenCode 53.
	if oc := agentOf(t, info.Agents, "opencode"); models != runnerwire.MaxModels || len(oc.Models) != 53 || oc.Models[52].ID != "provider/model-51" {
		t.Errorf("%d models in the hello, opencode %v", models, modelIDs(oc.Models))
	}
}

// The hub refuses a whole hello, and closes the connection, over one field
// longer than 256 bytes or not valid UTF-8. A CLI that fails with a long
// message, or one in another language cut mid-character, must not keep the
// runner offline.
func TestAgentRowsFitInAHello(t *testing.T) {
	settings := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(settings, []byte(`{"permissions":{"allow":["mcp(nodevas/*)"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	old := antigravitySettingsPath
	antigravitySettingsPath = func() (string, error) { return settings, nil }
	t.Cleanup(func() { antigravitySettingsPath = old })

	long := strings.Repeat("中", 150) // 450 bytes, 3 to a character
	cmds := &fakeCommands{
		paths: map[string]string{"claude": "/bin/claude", "codex": "/bin/codex", "agy": "/bin/agy"},
		outputs: map[string]string{
			"/bin/codex --version": "x" + long + "\n",
			"/bin/agy --version":   "1.0\n",
			"/bin/agy models":      "agy-1\t" + long + "\n",
		},
		errs: map[string]error{"/bin/claude --version": errors.New("exit status 1: " + long + "\xff")},
	}
	agents := Detect(context.Background(), cmds, nil)
	if agy := agentOf(t, agents, "antigravity"); !agy.Installed || len(agy.Models) != 2 {
		t.Fatalf("agy = %+v", agy)
	}
	for _, agent := range agents {
		fields := []string{agent.Version, agent.Error}
		for _, m := range agent.Models {
			fields = append(fields, m.ID, m.Label)
		}
		for _, field := range fields {
			if len(field) > 256 || !utf8.ValidString(field) {
				t.Errorf("%s: field the hub refuses (%d bytes): %q", agent.Kind, len(field), field)
			}
		}
	}
}

// The runner's log repeats why the hub closed the connection, when the hub
// said why.
func TestReadErrorNamesTheHubsReason(t *testing.T) {
	err := readError(fmt.Errorf("read: %w", websocket.CloseError{Code: websocket.StatusPolicyViolation, Reason: "expected hello"}))
	if err.Error() != "hub closed the connection (1008: expected hello)" {
		t.Errorf("with a reason: %v", err)
	}
	err = readError(websocket.CloseError{Code: websocket.StatusGoingAway})
	if err.Error() != "hub closed the connection (1001)" {
		t.Errorf("without one: %v", err)
	}
}
