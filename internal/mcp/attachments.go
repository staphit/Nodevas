package mcp

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"nodevas/internal/agents"
	"nodevas/internal/agentsetup"
)

// inboxDir is where save_attachment puts files, relative to the agent's
// working directory, and the line that keeps them out of git.
const (
	inboxDir         = ".nodevas/inbox"
	inboxExcludeLine = ".nodevas/inbox/"
)

// WorkDir is the folder this client treats as the agent's working directory.
func (c *Client) WorkDir() string { return c.workDir }

// OffNetwork is the excluded folder the working directory is in, or "".
func (c *Client) OffNetwork() string { return c.offNetwork }

func (c *Client) offNetworkError() error {
	return &APIError{
		Code: CodePermissionDenied,
		Message: fmt.Sprintf("this session is off the Nodevas agent network: its folder is inside %s, "+
			"which is excluded on this machine (`nodevas agents exclude list`). Nothing was sent", c.offNetwork),
	}
}

func invalid(format string, args ...any) error {
	return &APIError{Code: CodeInvalidArgument, Message: fmt.Sprintf(format, args...)}
}

// ReadAttachment reads the file at path as a message attachment: a regular
// file of UTF-8 text within the network's limits, named by its base name.
func ReadAttachment(path string) (agents.FileInput, error) {
	info, err := os.Stat(path)
	if err != nil {
		return agents.FileInput{}, invalid("cannot attach %s: %v", path, err)
	}
	if !info.Mode().IsRegular() {
		return agents.FileInput{}, invalid("cannot attach %s: not a regular file", path)
	}
	if info.Size() > agents.MaxFileBytes {
		return agents.FileInput{}, invalid("cannot attach %s: %d bytes is over the %d-byte limit", path, info.Size(), agents.MaxFileBytes)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return agents.FileInput{}, invalid("cannot attach %s: %v", path, err)
	}
	name := filepath.Base(path)
	if err := agents.ValidateFileName(name); err != nil {
		return agents.FileInput{}, invalid("cannot attach %s: %v", path, err)
	}
	if err := agents.ValidateFileContent(name, string(raw)); err != nil {
		return agents.FileInput{}, invalid("cannot attach %s: %v", path, err)
	}
	return agents.FileInput{Name: name, Content: string(raw)}, nil
}

// resolveInside resolves path (relative to dir unless absolute) and reports
// it only if it is dir or below it, through symlinks too: an agent talked
// into attaching ../../.ssh/id_rsa, or a link pointing there, must not ship
// it to a peer.
func resolveInside(dir, path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", invalid("an attachment path is empty")
	}
	if dir == "" {
		return "", invalid("no working directory to attach files from")
	}
	candidate := path
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(dir, candidate)
	}
	candidate = filepath.Clean(candidate)
	if !agentsetup.IsInside(candidate, dir) {
		return "", invalid("cannot attach %s: only files inside the working directory %s can be attached", path, dir)
	}
	real, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", invalid("cannot attach %s: %v", path, err)
	}
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		realDir = dir
	}
	if !agentsetup.IsInside(real, realDir) {
		return "", invalid("cannot attach %s: it resolves outside the working directory %s", path, dir)
	}
	return real, nil
}

// readWorkspaceFiles reads the files send_message attaches, each of which
// must be inside the working directory.
func (c *Client) readWorkspaceFiles(paths []string) ([]agents.FileInput, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	if len(paths) > agents.MaxFiles {
		return nil, invalid("at most %d files per message", agents.MaxFiles)
	}
	out := make([]agents.FileInput, 0, len(paths))
	for _, p := range paths {
		resolved, err := resolveInside(c.workDir, p)
		if err != nil {
			return nil, err
		}
		file, err := ReadAttachment(resolved)
		if err != nil {
			return nil, err
		}
		out = append(out, file)
	}
	return out, nil
}

var messageIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// portableFileName refuses names the server accepts but some file system
// would read as something else: Windows' reserved characters and device
// names, and trailing dots or spaces it silently drops.
func portableFileName(name string) error {
	if strings.ContainsAny(name, `<>:"|?*`) {
		return invalid("file name %q has a character that cannot be in a file name on every system", name)
	}
	if strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") {
		return invalid("file name %q ends in a dot or space", name)
	}
	stem := strings.ToUpper(strings.TrimSpace(strings.SplitN(name, ".", 2)[0]))
	switch stem {
	case "CON", "PRN", "AUX", "NUL",
		"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return invalid("file name %q is a reserved device name on Windows", name)
	}
	return nil
}

// SaveAttachment downloads one attachment of an inbox message to
// <workdir>/.nodevas/inbox/<messageID>/<name>. The copy is 0600 (readable by
// this user, executable by nobody), an existing file is never overwritten,
// and the folder is added to the repo's .git/info/exclude so it is not
// committed by accident.
func (c *Client) SaveAttachment(ctx context.Context, messageID, name string) (saveAttachmentOutput, error) {
	messageID = strings.TrimSpace(messageID)
	if !messageIDPattern.MatchString(messageID) {
		return saveAttachmentOutput{}, invalid("message_id %q is not a message id", messageID)
	}
	if err := agents.ValidateFileName(name); err != nil {
		return saveAttachmentOutput{}, invalid("%v", err)
	}
	if err := portableFileName(name); err != nil {
		return saveAttachmentOutput{}, err
	}
	if c.workDir == "" {
		return saveAttachmentOutput{}, invalid("no working directory to save into")
	}

	var body []byte
	q := url.Values{}
	if c.actor != "" {
		q.Set("actor", c.actor)
	}
	if err := c.get(ctx, "/api/agents/messages/"+url.PathEscape(messageID)+"/files/"+url.PathEscape(name), q, &body); err != nil {
		return saveAttachmentOutput{}, err
	}
	if len(body) > agents.MaxFileBytes || !utf8.Valid(body) {
		return saveAttachmentOutput{}, invalid("attachment %q is not text within the limits; not saved", name)
	}

	dir := filepath.Join(c.workDir, filepath.FromSlash(inboxDir), messageID)
	target := filepath.Join(dir, name)
	if !agentsetup.IsInside(target, dir) {
		return saveAttachmentOutput{}, invalid("attachment name %q would leave %s", name, dir)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return saveAttachmentOutput{}, err
	}
	// A .nodevas or inbox folder that is a link out of the working
	// directory would carry the write with it.
	if realDir, err := filepath.EvalSymlinks(dir); err == nil {
		realWork, werr := filepath.EvalSymlinks(c.workDir)
		if werr != nil {
			realWork = c.workDir
		}
		if !agentsetup.IsInside(realDir, realWork) {
			return saveAttachmentOutput{}, invalid("%s resolves outside the working directory; not saved", dir)
		}
	}
	f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return saveAttachmentOutput{}, &APIError{
			Code:    CodeConflict,
			Message: fmt.Sprintf("%s already exists; nothing was overwritten", target),
		}
	}
	if err != nil {
		return saveAttachmentOutput{}, err
	}
	if _, err := f.Write(body); err != nil {
		_ = f.Close()
		_ = os.Remove(target)
		return saveAttachmentOutput{}, err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(target)
		return saveAttachmentOutput{}, err
	}
	// Best effort: the file is saved either way, and a repo whose exclude
	// file cannot be written is not a reason to lose it.
	_ = excludeInboxFromGit(c.workDir)
	return saveAttachmentOutput{Path: target, Size: int64(len(body))}, nil
}

// findGitDir walks up from dir to the nearest repository and returns its
// root (the folder holding .git) and the directory info/exclude lives in:
// .git itself, or for a linked worktree the main repository's common dir.
func findGitDir(dir string) (root, common string, ok bool) {
	for {
		dotGit := filepath.Join(dir, ".git")
		info, err := os.Stat(dotGit)
		if err == nil {
			if info.IsDir() {
				return dir, dotGit, true
			}
			raw, err := os.ReadFile(dotGit)
			if err != nil {
				return "", "", false
			}
			gitDir, found := strings.CutPrefix(strings.TrimSpace(string(raw)), "gitdir:")
			if !found {
				return "", "", false
			}
			gitDir = strings.TrimSpace(gitDir)
			if !filepath.IsAbs(gitDir) {
				gitDir = filepath.Join(dir, gitDir)
			}
			common = gitDir
			if raw, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
				common = strings.TrimSpace(string(raw))
				if !filepath.IsAbs(common) {
					common = filepath.Join(gitDir, common)
				}
			}
			return dir, filepath.Clean(common), true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", "", false
		}
		dir = parent
	}
}

// excludeInboxFromGit adds the inbox folder to the repository's
// info/exclude, once. info/exclude rather than .gitignore: it is this
// clone's business, and editing a tracked file would itself be a change to
// commit.
func excludeInboxFromGit(workDir string) error {
	root, common, ok := findGitDir(workDir)
	if !ok {
		return nil
	}
	// A pattern with a slash in it is anchored to the repo root, so a
	// working directory below the root needs its own path in front.
	line := inboxExcludeLine
	if rel, err := filepath.Rel(root, workDir); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
		line = filepath.ToSlash(rel) + "/" + inboxExcludeLine
	}
	path := filepath.Join(common, "info", "exclude")
	existing, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for _, l := range strings.Split(string(existing), "\n") {
		if strings.TrimSpace(l) == line {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	prefix := ""
	if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
		prefix = "\n"
	}
	if _, err := f.WriteString(prefix + line + "\n"); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
