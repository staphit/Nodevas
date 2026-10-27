package runs

import (
	"path"
	"slices"
	"strings"
)

// sensitiveDirs are directories whose files change how code is built, tested
// or run (CI, hooks, scripts, agent instructions).
var sensitiveDirs = []string{
	".github", ".gitlab", ".circleci", ".buildkite", ".husky", ".devcontainer",
	".claude", ".cursor", ".vscode", "scripts",
}

// sensitiveFiles are manifests, lockfiles, build, CI and test-runner files,
// by base name (lower case).
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
	"pytest.ini": true,
	// Ruby, PHP, JVM, .NET.
	"gemfile": true, "gemfile.lock": true, "composer.json": true, "composer.lock": true,
	"pom.xml": true, "build.gradle": true, "build.gradle.kts": true, "settings.gradle": true,
	"settings.gradle.kts": true, "gradle-wrapper.properties": true, "nuget.config": true,
	"directory.build.props": true, "directory.build.targets": true, "packages.lock.json": true,
}

// testRunnerConfigs are base-name prefixes of test-runner configuration: a
// change there can skip or neuter the tests the test command runs.
var testRunnerConfigs = []string{
	"vitest.config.", "vitest.workspace.", "jest.config.", "playwright.config.", "cypress.config.",
	"karma.conf.", ".mocharc", "phpunit.xml",
}

// SensitivePath reports whether p is a path whose change can neuter the test
// command or the CI gate, or that an unattended merge must not change: CI and
// hook configuration, manifests and lockfiles, container, build and
// test-runner files, scripts, git configuration and agent instructions.
// Shared by the staged pipeline's diff guard and the autopilot's merge hold.
func SensitivePath(p string) bool {
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
	for _, prefix := range testRunnerConfigs {
		if strings.HasPrefix(base, prefix) {
			return true
		}
	}
	return false
}
