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
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

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
	Config   *Config
	Commands CommandRunner
	// Log receives one line per connection event; nil discards them.
	Log io.Writer

	// Timings, zero meaning the defaults. Tests shorten them.
	PingInterval     time.Duration
	RedetectInterval time.Duration
	BackoffMin       time.Duration
	BackoffMax       time.Duration
	// HTTPClient dials the hub; nil uses http.DefaultClient.
	HTTPClient *http.Client
}

const (
	defaultRedetect   = 5 * time.Minute
	defaultBackoffMin = time.Second
	defaultBackoffMax = time.Minute
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

// session is one connection: dial, hello, welcome, then ping and re-detect
// until something ends it. welcomed reports whether the hub accepted the
// runner, which is what resets the backoff.
func (c *Client) session(ctx context.Context) (welcomed bool, err error) {
	target, err := connectURL(c.Config.Hub)
	if err != nil {
		return false, err
	}
	header := http.Header{}
	header.Set("Authorization", "Bearer "+c.Config.Token)
	if c.Config.AccessClientID != "" {
		header.Set(runnerwire.AccessClientIDHeader, c.Config.AccessClientID)
		header.Set(runnerwire.AccessClientSecretHeader, c.Config.AccessClientSecret)
	}
	conn, resp, err := websocket.Dial(ctx, target, &websocket.DialOptions{
		HTTPHeader: header,
		HTTPClient: c.HTTPClient,
	})
	if err != nil {
		return false, dialError(resp, err)
	}
	defer conn.CloseNow()
	conn.SetReadLimit(runnerwire.MaxHelloBytes)

	info := BuildInfo(ctx, c.Commands, c.Config)
	if err := c.write(ctx, conn, runnerwire.Envelope{Type: runnerwire.TypeHello, Hello: &info}); err != nil {
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
					c.logf("connected to %s as %q (owner %s)", c.Config.Hub, msg.Welcome.Name, msg.Welcome.Owner)
				} else {
					c.logf("connected to %s", c.Config.Hub)
				}
			case runnerwire.TypeBye:
				return false, byeError(msg.Reason)
			}
		}
	}

	ping := time.NewTicker(c.pingInterval())
	defer ping.Stop()
	redetect := time.NewTicker(c.redetectInterval())
	defer redetect.Stop()
	lastHeard := time.Now()
	for {
		select {
		case <-ctx.Done():
			_ = conn.Close(websocket.StatusNormalClosure, "runner stopping")
			return true, ctx.Err()
		case err := <-readErr:
			return true, readError(err)
		case msg := <-incoming:
			lastHeard = time.Now()
			if msg.Type == runnerwire.TypeBye {
				return true, byeError(msg.Reason)
			}
		case <-ping.C:
			if time.Since(lastHeard) > 3*c.pingInterval() {
				return true, errors.New("hub stopped answering pings")
			}
			if err := c.write(ctx, conn, runnerwire.Envelope{Type: runnerwire.TypePing}); err != nil {
				return true, err
			}
		case <-redetect.C:
			next := BuildInfo(ctx, c.Commands, c.Config)
			if reflect.DeepEqual(next, info) {
				continue
			}
			info = next
			c.logf("agents or repositories changed; telling the hub")
			if err := c.write(ctx, conn, runnerwire.Envelope{Type: runnerwire.TypeHello, Hello: &info}); err != nil {
				return true, err
			}
		}
	}
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

func readError(err error) error {
	if status := websocket.CloseStatus(err); status != -1 {
		return fmt.Errorf("hub closed the connection (%d)", status)
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
	if c.Config.Name != "" {
		prefix = "runner " + c.Config.Name + ": "
	}
	fmt.Fprintf(c.Log, prefix+format+"\n", args...)
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
