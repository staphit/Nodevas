package copilot

import (
	"context"
	"errors"
	"strings"
	"testing"

	"nodevas/internal/copilot/localllm"
	"nodevas/internal/copilot/provider"
	"nodevas/internal/runnerwire"
)

// freeHarness is a server with no provider and no key configured.
func freeHarness(t *testing.T, mutate func(*Options)) *harness {
	return newHarness(t, func(opts *Options) {
		opts.Defaults = Defaults{Enabled: true, AllowBypass: true}
		opts.DefaultModel = provider.DefaultModel
		opts.NewProvider = func(cfg provider.Config) (provider.Provider, error) {
			return &fakeProvider{t: t, repeat: answer("local " + cfg.Model)}, nil
		}
		if mutate != nil {
			mutate(opts)
		}
	})
}

func localOllama(found bool) func(context.Context) (localllm.Found, bool) {
	return func(context.Context) (localllm.Found, bool) {
		if !found {
			return localllm.Found{}, false
		}
		return localllm.Found{Server: localllm.ServerOllama, BaseURL: "http://127.0.0.1:11434/v1", Model: "qwen3:8b"}, true
	}
}

func autoRunnerFor(owner string) *runnerwire.Runner {
	runner := runnerFor(owner)
	runner.Info.LLM = &runnerwire.LLMInfo{Provider: "openai", Model: "llama3.1:8b",
		BaseURL: "http://127.0.0.1:11434/v1", Auto: true, Local: localllm.ServerOllama}
	return runner
}

func status(t *testing.T, h *harness, browser string) Status {
	t.Helper()
	got, err := h.service.Status(context.Background(), alice, browser)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestFreeModelOrder(t *testing.T) {
	cases := []struct {
		name    string
		remote  *fakeRemote
		local   bool
		browser string
		want    func(Status) bool
	}{
		{"browser first", &fakeRemote{runner: autoRunnerFor(alice.ID)}, true, BrowserAvailable, func(s Status) bool {
			return s.Enabled && s.ModelHost == ModelHostBrowser && s.Provider == ProviderBrowser &&
				s.Model == BrowserModelName && s.Automatic && !s.AllowBypass
		}},
		{"runner's explicit provider beats the browser", &fakeRemote{runner: runnerFor(alice.ID)}, true, BrowserAvailable, func(s Status) bool {
			return s.Enabled && s.ModelHost == ModelHostRunner && s.Model == "claude-runner" && !s.Automatic
		}},
		{"runner's local model before the server's", &fakeRemote{runner: autoRunnerFor(alice.ID)}, true, "", func(s Status) bool {
			return s.Enabled && s.ModelHost == ModelHostRunner && s.Model == "llama3.1:8b" &&
				s.LocalServer == localllm.ServerOllama && s.Automatic && s.Runner == "desk"
		}},
		{"server's local model", &fakeRemote{}, true, "unavailable", func(s Status) bool {
			return s.Enabled && s.ModelHost == ModelHostServer && s.Provider == "openai" && s.Model == "qwen3:8b" &&
				s.LocalServer == localllm.ServerOllama
		}},
		{"local model rather than a download", nil, true, BrowserDownloadable, func(s Status) bool {
			return s.Enabled && s.Model == "qwen3:8b"
		}},
		{"download when nothing else", nil, false, BrowserDownloadable, func(s Status) bool {
			return !s.Enabled && s.NeedsDownload && s.Provider == ProviderBrowser
		}},
		{"nothing at all", &fakeRemote{}, false, "", func(s Status) bool {
			return !s.Enabled && s.NeedsSetup && s.ReasonCode == ReasonNoModel && !s.NeedsRunner &&
				strings.Contains(s.Reason, "Ollama") && strings.Contains(s.Reason, "Chrome 148")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := freeHarness(t, func(opts *Options) {
				if tc.remote != nil {
					opts.Remote = tc.remote
				}
				opts.LocalModels = localOllama(tc.local)
			})
			if got := status(t, h, tc.browser); !tc.want(got) {
				t.Fatalf("status = %+v", got)
			}
		})
	}
}

// Anything configured wins over the free order.
func TestExplicitSettingsWinOverFreeModels(t *testing.T) {
	// A key in the environment: its vendor, as before automatic existed.
	h := freeHarness(t, func(opts *Options) {
		opts.Defaults.VendorKeys = map[string]string{"openai": "sk-openai"}
		opts.DefaultModel = provider.DefaultModel
		opts.LocalModels = localOllama(true)
	})
	if got := status(t, h, BrowserAvailable); !got.Enabled || got.Provider != "openai" || got.Model != "gpt-5" ||
		got.ModelHost != ModelHostServer || got.Automatic {
		t.Fatalf("keyed status = %+v", got)
	}
	view, err := h.service.Settings(context.Background())
	if err != nil || !view.Automatic || view.Provider != "openai" {
		t.Fatalf("keyed view = %+v, %v", view, err)
	}

	// A provider an administrator chose: no free fallback, today's reason.
	h = freeHarness(t, func(opts *Options) { opts.LocalModels = localOllama(true) })
	anthropic := "anthropic"
	server := KeySourceServer
	if _, err := h.service.UpdateSettings(context.Background(), SettingsUpdate{Provider: &anthropic, KeySource: &server}); err != nil {
		t.Fatal(err)
	}
	if got := status(t, h, BrowserAvailable); got.Enabled || got.Provider != "anthropic" || got.NeedsSetup ||
		!strings.Contains(got.Reason, "API key") {
		t.Fatalf("chosen provider status = %+v", got)
	}

	// keySource runner: only a runner, never the browser or the server.
	h = freeHarness(t, func(opts *Options) {
		opts.Remote = &fakeRemote{}
		opts.LocalModels = localOllama(true)
	})
	runnerSource := KeySourceRunner
	if _, err := h.service.UpdateSettings(context.Background(), SettingsUpdate{KeySource: &runnerSource}); err != nil {
		t.Fatal(err)
	}
	if got := status(t, h, BrowserAvailable); got.Enabled || !got.NeedsRunner || got.ModelHost != ModelHostRunner {
		t.Fatalf("runner-only status = %+v", got)
	}

	// Back to automatic with the settings API.
	auto := ProviderAuto
	bad := "gemini"
	if _, err := h.service.UpdateSettings(context.Background(), SettingsUpdate{Provider: &bad}); !errors.Is(err, ErrInvalidSettings) {
		t.Fatalf("bad provider = %v", err)
	}
	if view, err := h.service.UpdateSettings(context.Background(), SettingsUpdate{Provider: &auto}); err != nil ||
		view.Provider != ProviderAuto || !view.Automatic {
		t.Fatalf("auto view = %+v, %v", view, err)
	}
}

// A turn on the server's local model calls it keyless at its loopback URL.
func TestTurnOnTheServersLocalModel(t *testing.T) {
	var built provider.Config
	h := freeHarness(t, func(opts *Options) {
		opts.LocalModels = localOllama(true)
		opts.NewProvider = func(cfg provider.Config) (provider.Provider, error) {
			built = cfg
			return &fakeProvider{t: t, repeat: answer("hello from ollama")}, nil
		}
	})
	conversation := h.conversation(alice, ModeDefault)
	events := h.start(TurnRequest{Actor: alice, ConversationID: conversation.ID, Text: "hi"}).finish()
	if last(events).Type != EventTurnFinished {
		t.Fatalf("events = %v", types(events))
	}
	if built.Kind != "openai" || built.Model != "qwen3:8b" || built.BaseURL != "http://127.0.0.1:11434/v1" || built.APIKey != "" {
		t.Fatalf("provider config = %+v", built)
	}
}

// A detector that reports something off this machine is not believed.
func TestNonLoopbackLocalModelIsIgnored(t *testing.T) {
	h := freeHarness(t, func(opts *Options) {
		opts.LocalModels = func(context.Context) (localllm.Found, bool) {
			return localllm.Found{Server: localllm.ServerOllama, BaseURL: "http://192.0.2.7:11434/v1", Model: "qwen3:8b"}, true
		}
	})
	if got := status(t, h, ""); got.Enabled || !got.NeedsSetup {
		t.Fatalf("status = %+v", got)
	}
}
