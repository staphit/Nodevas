package autopilot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path"
	"slices"
	"strconv"
	"strings"

	"nodevas/internal/runnerwire"
)

// contentHash is a task node's content as its runs see it: the title and
// the body the prompt is built from.
func contentHash(title, body string) string {
	sum := sha256.Sum256([]byte(title + "\x00" + body))
	return hex.EncodeToString(sum[:])
}

// sensitiveDirs are directories whose files change how code is built, tested
// or run (CI, hooks, scripts, agent instructions): a PR touching them is not
// merged without a person.
var sensitiveDirs = []string{
	".github", ".gitlab", ".circleci", ".buildkite", ".husky", ".devcontainer",
	".claude", ".cursor", ".vscode", "scripts",
}

// sensitiveFiles are manifests, lockfiles, build and CI files, by base name
// (lower case).
var sensitiveFiles = map[string]bool{
	// CI and hooks.
	".gitlab-ci.yml": true, ".travis.yml": true, "azure-pipelines.yml": true, "jenkinsfile": true,
	"bitbucket-pipelines.yml": true, ".drone.yml": true, "appveyor.yml": true,
	".pre-commit-config.yaml": true, ".pre-commit-config.yml": true, "lefthook.yml": true,
	// Containers.
	".dockerignore": true, "compose.yaml": true, "compose.yml": true,
	// Build.
	"makefile": true, "gnumakefile": true, "justfile": true, "taskfile.yml": true,
	// Agent instructions and tools.
	"claude.md": true, "agents.md": true, ".mcp.json": true,
	// JavaScript.
	"package.json": true, "package-lock.json": true, "npm-shrinkwrap.json": true, "yarn.lock": true,
	"pnpm-lock.yaml": true, "pnpm-workspace.yaml": true, "bun.lock": true, "bun.lockb": true,
	".npmrc": true, ".yarnrc": true, ".yarnrc.yml": true, ".pnpmfile.cjs": true,
	// Go.
	"go.mod": true, "go.sum": true, "go.work": true, "go.work.sum": true,
	// Rust.
	"cargo.toml": true, "cargo.lock": true, "build.rs": true,
	// Python.
	"pyproject.toml": true, "poetry.lock": true, "uv.lock": true, "pipfile": true, "pipfile.lock": true,
	"setup.py": true, "setup.cfg": true, "tox.ini": true, "noxfile.py": true, "conftest.py": true,
	// Ruby, PHP, JVM, .NET.
	"gemfile": true, "gemfile.lock": true, "composer.json": true, "composer.lock": true,
	"pom.xml": true, "build.gradle": true, "build.gradle.kts": true, "settings.gradle": true,
	"settings.gradle.kts": true, "gradle-wrapper.properties": true, "nuget.config": true,
	"directory.build.props": true, "directory.build.targets": true, "packages.lock.json": true,
}

// sensitivePath reports whether a changed file is one an unattended merge
// must not change: CI and hook configuration, manifests and lockfiles,
// container and build files, scripts, git configuration and agent
// instructions.
func sensitivePath(p string) bool {
	p = strings.ToLower(strings.TrimPrefix(strings.ReplaceAll(p, "\\", "/"), "./"))
	if p == "" {
		return false
	}
	parts := strings.Split(path.Clean(p), "/")
	for _, dir := range parts[:len(parts)-1] {
		if slices.Contains(sensitiveDirs, dir) {
			return true
		}
	}
	base := parts[len(parts)-1]
	switch {
	case sensitiveFiles[base]:
		return true
	case strings.HasPrefix(base, ".git"): // .gitignore, .gitattributes, .gitmodules
		return true
	case strings.HasPrefix(base, "dockerfile"), strings.HasSuffix(base, ".dockerfile"),
		strings.HasPrefix(base, "docker-compose"), strings.HasPrefix(base, "containerfile"):
		return true
	case strings.HasPrefix(base, "requirements") && strings.HasSuffix(base, ".txt"):
		return true
	}
	return false
}

// maxHoldPaths bounds the paths a hold's reason names.
const maxHoldPaths = 8

// mergeHold is why a try's PR must not merge without a person, or "": it
// changes a sensitive path, its changes are unknown or incomplete, or its
// head is not a commit the try made. It fails closed: when the changes
// cannot be read, it holds.
func (o *Orchestrator) mergeHold(ctx context.Context, obs observation) string {
	if obs.prRun == nil {
		return "the pull request's run is unknown"
	}
	var touched []string
	commits := map[string]bool{}
	reported := false
	for _, r := range obs.runs {
		if r.Kind != runnerwire.JobImplement && r.Kind != runnerwire.JobRevise {
			continue // review runs change nothing
		}
		ch, err := o.opts.Runs.Changes(ctx, r.ID)
		if err != nil {
			return "its changed files cannot be read: " + err.Error()
		}
		if ch == nil {
			continue
		}
		reported = true
		if len(ch.Files) >= runnerwire.MaxChangedFiles {
			return "it changes too many files to check"
		}
		if ch.Commit != "" {
			commits[ch.Commit] = true
		}
		for _, f := range ch.Files {
			for _, p := range []string{f.Path, f.OldPath} {
				if p != "" && sensitivePath(p) && !slices.Contains(touched, p) {
					touched = append(touched, p)
				}
			}
		}
	}
	if !reported {
		return "its runner reported no changed files"
	}
	if head := obs.prRun.PRHeadSHA; head != "" && !commits[head] {
		return "its head " + shortSHA(head) + " is not a commit the autopilot made"
	}
	if len(touched) == 0 {
		return ""
	}
	slices.Sort(touched)
	list := touched
	if len(list) > maxHoldPaths {
		list = list[:maxHoldPaths]
	}
	reason := "it changes " + strings.Join(list, ", ")
	if extra := len(touched) - len(list); extra > 0 {
		reason += " and " + strconv.Itoa(extra) + " more"
	}
	return reason
}
