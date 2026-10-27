package copilot

import (
	"context"
	"errors"

	"nodevas/internal/copilot/localllm"
	"nodevas/internal/identity"
	"nodevas/internal/runnerwire"
)

// Where a person's model calls run.
const (
	ModelHostServer  = "server"
	ModelHostRunner  = "runner"
	ModelHostBrowser = "browser"
)

// ReasonNoModel: automatic mode found nothing free to use and no key is
// configured. The panel explains the ways out in the person's language.
const ReasonNoModel = "no_model"

// noFreeModel is the reason behind ReasonNoModel.
const noFreeModel = "no model is available: use Chrome 148 or later on a desktop for its built-in model, " +
	"install Ollama and pull a model (ollama pull qwen3:8b), or set an API key"

// modelPlan is where one person's model calls go, decided per request.
type modelPlan struct {
	settings Settings
	key      string

	host     string
	provider string
	model    string
	baseURL  string
	// automatic: picked by the automatic order, not configured.
	automatic bool
	// local is the local server kind (localllm.Server*) when one was found.
	local string

	runner     runnerwire.Runner
	runnerInfo runnerwire.LLMInfo

	reason        string
	reasonCode    string
	needsRunner   bool
	needsDownload bool
	needsSetup    bool
}

// plan decides where actor's model calls go. browser is the speaker's own
// browser's Prompt API availability ("" when it has none).
//
// Explicit configuration always wins: a provider an administrator chose, a
// key on the server, keySource runner, or a provider set on the person's
// runner. Only with none of those (provider "auto", no key) does the free
// order apply:
//
//  1. the browser's built-in model, when the browser has it ready;
//  2. a local OpenAI-compatible server (Ollama, LM Studio) found on the
//     loopback of the machine that would call it: the speaker's runner
//     first, then this server;
//  3. the browser's model again when it only needs downloading;
//  4. otherwise a reason that says what to install or set.
func (s *Service) plan(ctx context.Context, actor identity.Actor, browser string) (modelPlan, error) {
	settings, key, _, err := s.settings.resolve(ctx)
	if err != nil {
		return modelPlan{}, err
	}
	p := modelPlan{
		settings: settings, key: key, host: ModelHostServer,
		provider: settings.Provider, model: settings.Model, baseURL: settings.BaseURL,
	}
	if settings.Provider != ProviderAuto {
		if effectiveKeySource(settings, key) == KeySourceRunner {
			p.host = ModelHostRunner
			if p.reason = s.baseUnavailableReason(); p.reason == "" {
				s.planRunner(ctx, actor, &p)
			}
		} else {
			p.reason = s.unavailableReason(settings, key)
		}
		return p, nil
	}
	if p.reason = s.baseUnavailableReason(); p.reason != "" {
		return p, nil
	}
	s.planFree(ctx, actor, browser, &p)
	return p, nil
}

// planRunner points p at the person's runner, or says why it cannot.
func (s *Service) planRunner(ctx context.Context, actor identity.Actor, p *modelPlan) {
	runner, info, err := s.runnerTarget(ctx, actor.ID)
	if err != nil {
		p.reason = err.Error()
		p.needsRunner = s.opts.Remote != nil
		return
	}
	p.useRunner(runner, info)
}

func (p *modelPlan) useRunner(runner runnerwire.Runner, info runnerwire.LLMInfo) {
	p.host = ModelHostRunner
	p.runner, p.runnerInfo = runner, info
	p.provider, p.model, p.baseURL = info.Provider, info.Model, info.BaseURL
	p.automatic, p.local = info.Auto, info.Local
	p.reason, p.needsRunner = "", false
}

func (s *Service) planFree(ctx context.Context, actor identity.Actor, browser string, p *modelPlan) {
	keySource := p.settings.KeySource
	var runnerErr error
	var autoRunner *runnerwire.Runner
	var autoInfo runnerwire.LLMInfo
	if keySource != KeySourceServer && s.opts.Remote != nil {
		runner, info, err := s.runnerTarget(ctx, actor.ID)
		switch {
		case err != nil:
			runnerErr = err
		case !info.Auto:
			// The person set a provider on their runner: their choice.
			p.useRunner(runner, info)
			return
		default:
			autoRunner, autoInfo = &runner, info
		}
	}
	if keySource == KeySourceRunner {
		// Model calls belong on runners: free choices elsewhere are not
		// this server's to make.
		if autoRunner != nil {
			p.useRunner(*autoRunner, autoInfo)
			return
		}
		p.host = ModelHostRunner
		if runnerErr == nil {
			runnerErr = errNoRemote
		}
		p.reason, p.needsRunner = runnerErr.Error(), s.opts.Remote != nil
		return
	}

	p.automatic = true
	if browser == BrowserAvailable {
		p.host, p.provider, p.model, p.baseURL = ModelHostBrowser, ProviderBrowser, BrowserModelName, ""
		return
	}
	if autoRunner != nil {
		p.useRunner(*autoRunner, autoInfo)
		return
	}
	if s.opts.LocalModels != nil && s.opts.NewProvider != nil {
		if found, ok := s.opts.LocalModels(ctx); ok && localllm.Loopback(found.BaseURL) {
			p.host, p.provider, p.model, p.baseURL = ModelHostServer, "openai", found.Model, found.BaseURL
			p.local = found.Server
			return
		}
	}
	if browser == BrowserDownloadable || browser == BrowserDownloading {
		p.host, p.provider, p.model, p.baseURL = ModelHostBrowser, ProviderBrowser, BrowserModelName, ""
		p.reason = "download the browser's on-device model to start"
		p.needsDownload = true
		return
	}
	p.host, p.provider, p.model = "", ProviderAuto, ""
	p.reason, p.reasonCode, p.needsSetup = noFreeModel, ReasonNoModel, true
}

// allowBypass: bypassPermissions is never offered with the browser's model.
// A small on-device model is the easiest to steer with text planted in a
// node, so every change it wants still goes past the person.
func (p *modelPlan) allowBypass() bool {
	return p.settings.AllowBypass && p.host != ModelHostBrowser
}

// errBypassBrowser refuses a bypassPermissions turn on the browser's model.
var errBypassBrowser = errors.New("bypassPermissions is not available with the browser's built-in model: " +
	"a small on-device model is easy to mislead with text inside nodes, so it always asks before changes; " +
	"pick acceptEdits or default")

// status turns a plan into what the panel reads.
func (p *modelPlan) status() Status {
	status := Status{
		Provider:      p.provider,
		Model:         p.model,
		Modes:         availableModes(p.allowBypass()),
		AllowBypass:   p.allowBypass(),
		Reason:        p.reason,
		ReasonCode:    p.reasonCode,
		KeySource:     p.host,
		ModelHost:     p.host,
		Automatic:     p.automatic,
		LocalServer:   p.local,
		NeedsRunner:   p.needsRunner,
		NeedsDownload: p.needsDownload,
		NeedsSetup:    p.needsSetup,
	}
	if p.host == ModelHostRunner && p.reason == "" {
		status.Runner = p.runner.Name
	}
	if status.KeySource == "" {
		status.KeySource = KeySourceRunner
	}
	status.Enabled = status.Reason == ""
	return status
}
