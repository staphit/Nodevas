package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"nodevas/internal/copilot/provider"
	"nodevas/internal/runner/adapter"
	"nodevas/internal/runnerwire"
)

// ErrRevoked stops the runner: the hub no longer accepts its token, and
// reconnecting would only be refused again.
var ErrRevoked = errors.New("runner token was revoked; run nodevas runner login again")

// ErrReplaced stops the runner: another runner connected with the same token.
// Reconnecting would knock that one off in turn, forever.
var ErrReplaced = errors.New("another runner connected with this token; stopping this one")

// Client keeps one runner connected to its hub.
type Client struct {
	// Config is the settings the runner starts with. Once it runs, a change
	// from the app replaces them whole (localsettings.go): read them with
	// cfg(), never from here.
	Config *Config
	// ConfigPath is the settings file Config was read from: a maxConcurrent
	// the owner changes from the hub (settings.go) is saved there. "" keeps
	// such a change until the runner stops.
	ConfigPath string
	Commands   CommandRunner
	// Log receives one line per connection event; nil discards them.
	Log io.Writer

	// Timings, zero meaning the defaults. Tests shorten them.
	PingInterval     time.Duration
	RedetectInterval time.Duration
	BackoffMin       time.Duration
	BackoffMax       time.Duration
	// versionTimeout and catalogTimeout bound the commands that look at the
	// agent CLIs (detect.go), checkAgainWait a "check again" from the app
	// and signInRecheck how often a signed-out agent is asked whether it is
	// signed in again (detector.go); zero means the defaults. Tests shorten
	// them.
	versionTimeout time.Duration
	catalogTimeout time.Duration
	checkAgainWait time.Duration
	signInRecheck  time.Duration
	// HTTPClient dials the hub and carries the MCP proxy's requests; nil uses
	// http.DefaultClient for the socket and a client without a timeout for
	// the proxy.
	HTTPClient *http.Client
	// ServeManaged is true for the runner `nodevas serve` started for itself
	// (NODEVAS_RUNNER_UNDER_SERVE): serve holds the hub's port for as long
	// as this runner runs.
	ServeManaged bool
	// Edition is the edition serve started this runner for
	// (NODEVAS_RUNNER_EDITION, read once by the CLI): runnerwire.EditionSingle
	// for a single-user hub's built-in runner (builtin.go), else "".
	Edition string
	// App asks for the owner's OK in the app, for the built-in runner
	// (appaccept.go); nil makes one that asks in the app only.
	App *AppAccepter

	// Accepter asks whether to take an offer the auto-accept policy does not
	// cover; nil declines those.
	Accepter Accepter
	// Adapter returns the agent adapter for a kind; nil means adapter.For.
	// Tests swap in a fake agent.
	Adapter func(kind string) adapter.Adapter
	// FlushInterval batches run events; zero means 250ms.
	FlushInterval time.Duration
	// RunTimeout overrides the config's run timeout; tests shorten it.
	RunTimeout time.Duration
	// runTimer starts the timer that ends a run at its timeout; nil means
	// time.NewTimer. Tests fire it themselves once the run is where they
	// want it to time out, instead of racing a short timeout.
	runTimer func(time.Duration) (expired <-chan time.Time, stop func() bool)
	// interactiveQuietTimeout, endQuitGap and endGrace override interactive
	// session quiet timeout and graceful-End timings in tests; zero uses the
	// defaults in interactive.go.
	interactiveQuietTimeout time.Duration
	endQuitGap              time.Duration
	endGrace                time.Duration
	// interactiveWaiting, if set, is called once an interactive run's
	// terminal is up, right before the runner starts waiting on it: tests
	// line up an exit and an End there to see which one wins.
	interactiveWaiting func(runID string)
	// interactiveExited, if set, is called once an interactive run's CLI
	// exit is ready for the wait to take; interactiveEnd, once the wait has
	// taken an End and before it looks for that exit. Tests line up an exit
	// that is there by the time an End is handled.
	interactiveExited func(runID string)
	interactiveEnd    func(runID string)
	// touchedPoll overrides how often a run's touched-files watcher polls
	// git status; zero uses touchedPollInterval. Tests shorten it.
	touchedPoll time.Duration
	// PRPollInterval overrides the config's PR polling interval; tests
	// shorten it.
	PRPollInterval time.Duration
	// NewTerminal starts an interactive run's agent in a terminal; nil means
	// a real pseudo-terminal. Tests swap in a fake.
	NewTerminal TerminalStarter
	// NewLLMProvider builds the copilot's model provider (llm.go); nil means
	// provider.New. Tests swap in a fake.
	NewLLMProvider func(provider.Config) (provider.Provider, error)

	jobs *jobs
	// limiter caps copilot model calls per minute across connections.
	limiter llmLimiter
	// maxRuns is maxConcurrent as the hub last set it (settings.go); 0 until
	// then, the config's applying. settingsMu orders such changes.
	maxRuns    atomic.Int32
	settingsMu sync.Mutex
	appOnce    sync.Once
	adminOnce  sync.Once

	// live is the config in effect (cfg): Config until a change from the
	// app replaces it whole. Each run keeps the one it was offered under.
	live atomic.Pointer[Config]
	// builtIn: this connection's runner is the built-in one (builtin.go);
	// builtInState remembers the last decision (1 classic, 2 built-in) so a
	// reconnect logs only a change.
	builtIn      atomic.Bool
	builtInState atomic.Int32
	// detect is what the runner knows about the agents on this machine,
	// looked at off the connection (detector.go).
	detect *detector
	// infoChanged asks the connection for a new hello: the settings the app
	// shows changed outside a settings request (localsettings.go), or a look
	// at this machine found something new.
	infoChanged chan struct{}
	// admin lets one repo admin operation run at a time (repoadmin.go).
	admin chan struct{}
	// guard replaces the sensitive-folder rules in tests (repoguard.go).
	guard *repoGuard
	// preview is the built-in runner's result preview listener (preview.go),
	// started on the first page minted and stopped when Run returns.
	preview previewSlot
}

// cfg is the config in effect now. A run uses the one it was offered under
// (run.cfg) instead, so a change from the app applies to the runs after it
// and never to one already going.
func (c *Client) cfg() *Config {
	if cfg := c.live.Load(); cfg != nil {
		return cfg
	}
	return c.Config
}

// app is the AppAccepter, made on first use.
func (c *Client) app() *AppAccepter {
	c.appOnce.Do(func() {
		if c.App == nil {
			c.App = &AppAccepter{}
		}
	})
	return c.App
}

// changedInfo asks the connection for a new hello.
func (c *Client) changedInfo() {
	if c.infoChanged == nil {
		return
	}
	select {
	case c.infoChanged <- struct{}{}:
	default:
	}
}

func (c *Client) llmLimiter() *llmLimiter { return &c.limiter }

const (
	defaultRedetect   = 5 * time.Minute
	defaultBackoffMin = time.Second
	defaultBackoffMax = time.Minute
	defaultFlush      = 250 * time.Millisecond
	welcomeTimeout    = 15 * time.Second
	writeTimeout      = 10 * time.Second
)

// Run connects and stays connected until ctx ends, reconnecting with
// exponential backoff after a dropped connection. It returns nil when ctx is
// cancelled, and ErrRevoked or ErrReplaced when the hub says to stop.
func (c *Client) Run(ctx context.Context) error {
	if err := ValidateToken(c.Config.Token); err != nil {
		return err
	}
	if _, err := ValidateHub(c.Config.Hub); err != nil {
		return err
	}
	if err := c.Config.PlainLocalHubRefusal(c.ServeManaged); err != nil {
		return err
	}
	c.live.Store(c.Config)
	c.infoChanged = make(chan struct{}, 1)
	defer c.preview.close()
	if c.Config.LegacyAutoAccept() {
		// The CLI migrates and saves before this; a Config built elsewhere
		// is migrated here, in memory.
		if warning := c.Config.MigrateLegacyAutoAccept(InstalledKinds(ctx, c.Commands, c.Config)); warning != "" {
			c.logf("%s", warning)
		}
	}
	if c.Config.AutoAcceptOn() {
		c.logf("%s", AutoAcceptWarning)
		if !c.Config.AutoAcceptPolicy().RequireStepUp {
			c.logf("%s", StepUpOptionalWarning)
		}
	}
	c.jobs = newJobs(ctx, c)
	defer c.jobs.shutdown()
	c.jobs.loadPRs()
	c.jobs.loadMerges()
	c.jobs.loadWorkspaceActions()
	c.jobs.wg.Add(1)
	go c.jobs.watchPRs(c.prPollInterval)
	c.detect = newDetector(c)
	detectCtx, stopDetecting := context.WithCancel(ctx)
	detecting := make(chan struct{})
	go func() {
		defer close(detecting)
		c.detect.run(detectCtx)
	}()
	defer func() {
		stopDetecting()
		<-detecting
	}()
	// The first look at this machine comes before the first connection, and
	// only that one: no hub is waiting yet, so nothing can time out. The
	// first hello must say what is really here. The hub offers queued runs
	// right after it, and declining one because its agent or model was not
	// known yet is final; the built-in runner's one-time settings carry-over
	// (migrateBuiltIn) reads the installed agents from it too. Every later
	// hello says what the last look found, at once.
	select {
	case <-c.detect.first:
	case <-ctx.Done():
		return nil
	}
	backoff := c.backoffMin()
	for {
		welcomed, err := c.session(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if errors.Is(err, ErrRevoked) || errors.Is(err, ErrReplaced) {
			return err
		}
		if welcomed {
			backoff = c.backoffMin()
		}
		wait := jitter(backoff)
		c.logf("disconnected: %v; retrying in %s", err, wait.Round(100*time.Millisecond))
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(wait):
		}
		backoff = min(backoff*2, c.backoffMax())
	}
}

// session is one connection: dial, hello, welcome, then ping until something
// ends it. Nothing here waits for an agent CLI: every hello says what the
// last look at this machine found (detector.go). welcomed reports whether
// the hub accepted the runner, which is what resets the backoff.
func (c *Client) session(ctx context.Context) (welcomed bool, err error) {
	cfg := c.cfg()
	target, err := connectURL(cfg.Hub)
	if err != nil {
		return false, err
	}
	header := http.Header{}
	header.Set("Authorization", "Bearer "+cfg.Token)
	if cfg.AccessClientID != "" {
		header.Set(runnerwire.AccessClientIDHeader, cfg.AccessClientID)
		header.Set(runnerwire.AccessClientSecretHeader, cfg.AccessClientSecret)
	}
	conn, resp, err := websocket.Dial(ctx, target, &websocket.DialOptions{
		HTTPHeader: header,
		HTTPClient: c.HTTPClient,
	})
	if err != nil {
		return false, dialError(resp, err)
	}
	defer conn.CloseNow()
	// Offers carry prompts of up to 64 KB; the hub caps every message at this,
	// except a copilot model call, which carries a whole conversation.
	conn.SetReadLimit(max(runnerwire.MaxMessageBytes, runnerwire.MaxLLMRequestBytes))

	// Until the welcome settles it (builtin.go), this runner is the built-in
	// one only if the hub said so last time and its own side still holds:
	// never on its own word, so a run reaching its question before a welcome
	// that never comes is asked as a classic runner's would be.
	claim, _ := c.builtInClaim()
	c.builtIn.Store(claim && c.builtIn.Load())
	info := c.info()
	if err := c.hello(ctx, conn, info); err != nil {
		return false, err
	}

	// One reader goroutine; every write happens on this goroutine.
	incoming := make(chan runnerwire.Envelope)
	readErr := make(chan error, 1)
	readCtx, stopReading := context.WithCancel(ctx)
	defer stopReading()
	go func() {
		for {
			var msg runnerwire.Envelope
			if err := wsjson.Read(readCtx, conn, &msg); err != nil {
				readErr <- err
				return
			}
			select {
			case incoming <- msg:
			case <-readCtx.Done():
				return
			}
		}
	}()

	welcomeDeadline := time.NewTimer(welcomeTimeout)
	defer welcomeDeadline.Stop()
	ownerID := ""
	for !welcomed {
		select {
		case <-ctx.Done():
			_ = conn.Close(websocket.StatusNormalClosure, "runner stopping")
			return false, ctx.Err()
		case err := <-readErr:
			return false, readError(err)
		case <-welcomeDeadline.C:
			return false, errors.New("hub did not answer the hello")
		case msg := <-incoming:
			switch msg.Type {
			case runnerwire.TypeWelcome:
				welcomed = true
				if msg.Welcome != nil {
					ownerID = msg.Welcome.OwnerID
					c.jobs.setHubName(msg.Welcome.Name)
					c.logf("connected to %s as %q (owner %s)", cfg.Hub, msg.Welcome.Name, msg.Welcome.Owner)
				} else {
					c.logf("connected to %s", cfg.Hub)
				}
				if c.decideBuiltIn(msg.Welcome) {
					c.migrateBuiltIn(info.Agents)
				}
			case runnerwire.TypeBye:
				return false, byeError(msg.Reason)
			}
		}
	}

	// Copilot model calls on this connection (llm.go); stopped with it.
	llm := c.newLLMSession(ctx, ownerID)
	defer llm.stop()
	// Repo browsing answers (repobrowse.go) go out on this connection.
	repoOut := make(chan runnerwire.Envelope, 8)
	// So do settings answers (settings.go) and repo admin answers
	// (repoadmin.go), each after a hello.
	settingsOut := make(chan runnerwire.SettingsDone, 4)
	adminOut := make(chan repoAdminAnswer, 4)
	ping := time.NewTicker(c.pingInterval())
	defer ping.Stop()
	flushTick := time.NewTicker(c.flushInterval())
	defer flushTick.Stop()
	lastHeard := time.Now()
	// The welcome settled what the first hello could only claim: say so when
	// they differ (builtin.go).
	if next := c.refreshInfo(info); !reflect.DeepEqual(next, info) {
		info = next
		if err := c.hello(ctx, conn, info); err != nil {
			return true, err
		}
	}
	// Whatever runs said while the runner was away goes out first, then the
	// latest of every watched PR, once, in case the hub missed a change; and
	// every offer still waiting for a person, so the hub says why again.
	c.jobs.resendPRs()
	c.jobs.resendAsks()
	flush := func() error {
		write := func(msg runnerwire.Envelope) error { return c.write(ctx, conn, msg) }
		// Terminal output first, so a run's last screen arrives before its finish.
		if err := c.jobs.terms.flush(write); err != nil {
			return err
		}
		return c.jobs.flush(write)
	}
	if err := flush(); err != nil {
		return true, err
	}
	for {
		select {
		case <-ctx.Done():
			_ = conn.Close(websocket.StatusNormalClosure, "runner stopping")
			return true, ctx.Err()
		case err := <-readErr:
			return true, readError(err)
		case msg := <-incoming:
			lastHeard = time.Now()
			switch msg.Type {
			case runnerwire.TypeBye:
				return true, byeError(msg.Reason)
			case runnerwire.TypeOffer:
				c.jobs.offer(msg.Job)
			case runnerwire.TypeCancel:
				c.jobs.cancelRun(msg.RunID, msg.Reason)
			case runnerwire.TypeEnd:
				c.jobs.endRun(msg.RunID, msg.NoPR)
			case runnerwire.TypeMerge:
				c.jobs.merge(msg.RunID, msg.Merge)
			case runnerwire.TypeWorkspaceAction:
				c.jobs.workspaceAction(ownerID, msg.WorkspaceAction)
			case runnerwire.TypeTermIn:
				c.jobs.termInput(msg.RunID, msg.Term)
			case runnerwire.TypeTermResize:
				c.jobs.termResize(msg.RunID, msg.Term)
			case runnerwire.TypeLLMRequest:
				llm.request(msg.LLM)
			case runnerwire.TypeLLMCancel:
				llm.cancel(msg.LLMID)
			case runnerwire.TypeDecideRequest:
				llm.decide(msg.Decide)
			case runnerwire.TypeSettingsRequest:
				if msg.Settings != nil {
					req := *msg.Settings
					go func() {
						done := c.applySettings(ownerID, req)
						if req.Redetect && done.Code == "" {
							// Look for the agent CLIs again before answering,
							// but not for longer than the hub waits for the
							// answer: a look that outlasts it still ends in a
							// hello of its own.
							c.detect.again(ctx, c.checkAgainTimeout())
						}
						select {
						case settingsOut <- done:
						case <-ctx.Done():
						}
					}()
				}
			case runnerwire.TypeOfferAnswer:
				// The built-in runner's, from its owner only (appaccept.go).
				c.offerAnswer(ownerID, msg.OfferAnswer)
			case runnerwire.TypeRepoAdmin:
				if msg.RepoAdmin != nil {
					req := *msg.RepoAdmin
					go func() {
						answer := c.repoAdmin(ctx, ownerID, req)
						select {
						case adminOut <- answer:
						case <-ctx.Done():
						}
					}()
				}
			case runnerwire.TypeRepoRequest:
				if msg.Repo != nil {
					req := *msg.Repo
					go func() {
						var done runnerwire.RepoDone
						if runnerwire.RunRepoOp(req.Op) {
							// One run's worktree (workspace.go).
							done = c.jobs.browseRun(ctx, ownerID, req)
						} else {
							done = BrowseRepo(ctx, c.cfg(), ownerID, req)
						}
						select {
						case repoOut <- runnerwire.Envelope{Type: runnerwire.TypeRepoDone, RepoDone: &done}:
						case <-ctx.Done():
						}
					}()
				}
			}
		case env := <-llm.out:
			if err := c.write(ctx, conn, env); err != nil {
				return true, err
			}
		case env := <-repoOut:
			if err := c.write(ctx, conn, env); err != nil {
				return true, err
			}
		case done := <-settingsOut:
			// The hello with the settings in effect first: by the time the
			// hub hears the change is done, it offers by the new limit. It
			// says what the last look at the agent CLIs found, never a new
			// look: asking every CLI again can take longer than the hub
			// waits for the answer. When the owner asked to check again,
			// that look is already over (or took too long to wait for).
			info = c.info()
			if err := c.hello(ctx, conn, info); err != nil {
				return true, err
			}
			if err := c.write(ctx, conn, runnerwire.Envelope{Type: runnerwire.TypeSettingsDone, SettingsDone: &done}); err != nil {
				return true, err
			}
		case answer := <-adminOut:
			// As settings: the new repos in a hello first, so the hub knows a
			// repo by the time it hears it was added.
			if answer.changed {
				info = c.info()
				if err := c.hello(ctx, conn, info); err != nil {
					return true, err
				}
			}
			done := answer.done
			if err := c.write(ctx, conn, runnerwire.Envelope{Type: runnerwire.TypeRepoAdminDone, RepoAdminDone: &done}); err != nil {
				return true, err
			}
		case <-c.infoChanged:
			// Only when it says something new: a hello already sent for
			// another reason may have said it.
			if next := c.info(); !reflect.DeepEqual(next, info) {
				info = next
				if err := c.hello(ctx, conn, info); err != nil {
					return true, err
				}
			}
		case <-c.jobs.notify:
			if err := flush(); err != nil {
				return true, err
			}
		case <-c.jobs.terms.notifyCh():
			if err := c.jobs.terms.flush(func(msg runnerwire.Envelope) error { return c.write(ctx, conn, msg) }); err != nil {
				return true, err
			}
		case <-flushTick.C:
			if err := flush(); err != nil {
				return true, err
			}
		case <-ping.C:
			if time.Since(lastHeard) > 3*c.pingInterval() {
				return true, errors.New("hub stopped answering pings")
			}
			if err := c.write(ctx, conn, runnerwire.Envelope{Type: runnerwire.TypePing}); err != nil {
				return true, err
			}
		}
	}
}

// hello describes the runner, with the runs it is still executing so the hub
// fails only the ones that really died with a previous connection.
func (c *Client) hello(ctx context.Context, conn *websocket.Conn, info runnerwire.Info) error {
	c.jobs.setAgents(info.Agents)
	return c.write(ctx, conn, runnerwire.Envelope{Type: runnerwire.TypeHello, Hello: &info, Active: c.jobs.active()})
}

func (c *Client) write(ctx context.Context, conn *websocket.Conn, msg runnerwire.Envelope) error {
	writeCtx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return wsjson.Write(writeCtx, conn, msg)
}

// connectURL turns the hub's base URL into its runner socket URL.
func connectURL(hub string) (string, error) {
	u, err := url.Parse(hub)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	default:
		return "", fmt.Errorf("hub URL %q must be https", hub)
	}
	u.Path = strings.TrimRight(u.Path, "/") + runnerwire.ConnectPath
	return u.String(), nil
}

// dialError explains a refused connection in terms of what to do about it.
func dialError(resp *http.Response, err error) error {
	if resp == nil {
		return err
	}
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return ErrRevoked
	case http.StatusForbidden:
		if resp.Header.Get("Cf-Ray") != "" || strings.Contains(strings.ToLower(resp.Header.Get("Server")), "cloudflare") {
			return errors.New("Cloudflare Access refused the connection (403): check the service token " +
				"(nodevas runner login --access-client-id …) and that the Access application has a Service Auth policy for it")
		}
		return fmt.Errorf("hub refused the connection (403): %w", err)
	}
	return fmt.Errorf("hub answered %s: %w", resp.Status, err)
}

// readError explains a connection that ended, with the hub's own reason when
// it gave one: "expected hello" is what tells a late hello from a restart.
func readError(err error) error {
	var closed websocket.CloseError
	if errors.As(err, &closed) {
		if closed.Reason != "" {
			return fmt.Errorf("hub closed the connection (%d: %s)", closed.Code, closed.Reason)
		}
		return fmt.Errorf("hub closed the connection (%d)", closed.Code)
	}
	var syntax *json.SyntaxError
	if errors.As(err, &syntax) {
		return fmt.Errorf("hub sent an unreadable message: %w", err)
	}
	return err
}

func byeError(reason string) error {
	switch reason {
	case runnerwire.ByeRevoked:
		return ErrRevoked
	case runnerwire.ByeReplaced:
		return ErrReplaced
	}
	return fmt.Errorf("hub closed the connection: %s", reason)
}

// jitter spreads reconnects between half and all of the backoff, so runners
// dropped by one hub restart do not all return in the same instant.
func jitter(d time.Duration) time.Duration {
	half := d / 2
	if half <= 0 {
		return d
	}
	return half + rand.N(half+1)
}

func (c *Client) logf(format string, args ...any) {
	if c.Log == nil {
		return
	}
	prefix := "runner: "
	if name := c.cfg().Name; name != "" {
		prefix = "runner " + name + ": "
	}
	// One line, whatever it quotes: run ids, names and errors come from the
	// hub or an agent, and reach the terminal next to the accept question.
	fmt.Fprintln(c.Log, prefix+terminalText(fmt.Sprintf(format, args...)))
}

func (c *Client) pingInterval() time.Duration {
	if c.PingInterval > 0 {
		return c.PingInterval
	}
	return runnerwire.PingIntervalSeconds * time.Second
}

func (c *Client) redetectInterval() time.Duration {
	if c.RedetectInterval > 0 {
		return c.RedetectInterval
	}
	return defaultRedetect
}

func (c *Client) signInRecheckInterval() time.Duration {
	if c.signInRecheck > 0 {
		return c.signInRecheck
	}
	return defaultSignInRecheck
}

func (c *Client) checkAgainTimeout() time.Duration {
	if c.checkAgainWait > 0 {
		return c.checkAgainWait
	}
	return defaultCheckAgain
}

func (c *Client) flushInterval() time.Duration {
	if c.FlushInterval > 0 {
		return c.FlushInterval
	}
	return defaultFlush
}

// startRunTimer starts the timer that ends a run after timeout.
func (c *Client) startRunTimer(timeout time.Duration) (<-chan time.Time, func() bool) {
	if c.runTimer != nil {
		return c.runTimer(timeout)
	}
	timer := time.NewTimer(timeout)
	return timer.C, timer.Stop
}

// runTimeout is how long a run of kind may take under cfg, the run's own.
func (c *Client) runTimeout(cfg *Config, kind string) time.Duration {
	if c.RunTimeout > 0 {
		return c.RunTimeout
	}
	return cfg.RunTimeoutFor(kind)
}

func (c *Client) prPollInterval() time.Duration {
	if c.PRPollInterval > 0 {
		return c.PRPollInterval
	}
	return c.cfg().PRPollInterval()
}

func (c *Client) adapterFor(kind string) adapter.Adapter {
	return c.adapterUnder(c.cfg(), kind)
}

// adapterUnder is adapterFor under cfg, a run's own.
func (c *Client) adapterUnder(cfg *Config, kind string) adapter.Adapter {
	if c.Adapter != nil {
		return c.Adapter(kind)
	}
	if kind == "opencode" && cfg != nil && cfg.OpenCodeProtocol == "acp" {
		return adapter.OpenCodeACP{}
	}
	return adapter.For(kind)
}

func (c *Client) backoffMin() time.Duration {
	if c.BackoffMin > 0 {
		return c.BackoffMin
	}
	return defaultBackoffMin
}

func (c *Client) backoffMax() time.Duration {
	if c.BackoffMax > 0 {
		return c.BackoffMax
	}
	return defaultBackoffMax
}

func (c *Client) quietTimeout() time.Duration {
	if c != nil && c.interactiveQuietTimeout > 0 {
		return c.interactiveQuietTimeout
	}
	return interactiveQuiet
}

func (c *Client) quitGap() time.Duration {
	if c != nil && c.endQuitGap > 0 {
		return c.endQuitGap
	}
	return endQuitGap
}

func (c *Client) quitGrace() time.Duration {
	if c != nil && c.endGrace > 0 {
		return c.endGrace
	}
	return endGrace
}

// touchedOptions is a run's touched-files watcher settings; zero fields take
// the defaults in touched.go.
func (c *Client) touchedOptions(interactive bool) touchedOptions {
	opts := touchedOptions{interactive: interactive}
	if c != nil {
		opts.pollInterval = c.touchedPoll
	}
	return opts
}
