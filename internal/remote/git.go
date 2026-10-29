package remote

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"nodevas/internal/gitrepo"
	"nodevas/internal/locale"
	"nodevas/internal/secrets"
)

// Backing up to a git remote (docs/history-and-backup.md, "Backup to a git
// remote").
//
// Each project keeps its own history in <project>/.vised/git (see
// internal/gitrepo). The git backend pushes that history rather than a
// bundle: every project with a commit goes to refs/heads/nodevas/<project>
// of one HTTPS repository. A push is never forced. A branch that moved on
// the remote is reported and left alone; "keep local" pushes to a side
// branch instead.
//
// It needs git 2.31 or newer for GIT_CONFIG_COUNT, which is how the
// credentials reach git without appearing on its command line.

const (
	gitBranchPrefix = "nodevas/"
	// gitTrackingPrefix holds a fetched remote branch outside refs/heads and
	// refs/remotes, so nothing that lists branches ever shows it.
	gitTrackingPrefix = "refs/nodevas/remote/"
	// defaultGitUser is what GitHub expects beside a token; most other hosts
	// ignore the user when the password is a token.
	defaultGitUser = "x-access-token"

	maxGitURLLength   = 2048
	maxGitUserLength  = 256
	maxGitTokenLength = 4096
	maxGitRefLength   = 200
)

var (
	ErrGitTokenRequired = locale.Errorf("the git token must be entered again when the URL or the user changes", "網址或使用者變更時須重新輸入 git 權杖")
	ErrNoGitHistory     = locale.Errorf("there is no history to push yet", "還沒有可推送的歷史紀錄")
	ErrGitNoBundles     = locale.Errorf("a git remote holds branches, not bundles: clone the branch to restore it", "git 遠端保存的是分支而不是套件：請 clone 分支來還原")
)

// allowLocalGitURLs lets the tests push to a bare repository on disk (a path
// or file://) without DNS. Only tests set it; production accepts https only.
var allowLocalGitURLs = false

// runGit is the single place a git command leaves this package, so a test can
// see every argument and environment variable it is given.
var runGit = func(ctx context.Context, repo *gitrepo.Repo, env []string, args ...string) ([]byte, error) {
	return repo.Run(ctx, env, args...)
}

// lookupGitHost resolves the remote's host; a test replaces it to play a DNS
// answer that points somewhere private.
var lookupGitHost = func(ctx context.Context, host string) ([]net.IP, error) {
	return net.DefaultResolver.LookupIP(ctx, "ip", host)
}

// gitTarget is a validated remote: the URL git is given, and the host and
// port the address check and pinning work on. host is empty only for the
// tests' local paths.
type gitTarget struct {
	url  string
	host string
	port string
}

// parseGitURL accepts an https:// URL and nothing else: ssh would bring host
// key prompts, git:// and http:// send everything in the clear, and file://
// (or a bare path) would let a network user read any repository on the
// server's disk. The error never repeats the URL, which may hold a password.
func parseGitURL(raw string) (gitTarget, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return gitTarget{}, locale.Errorf("the git remote URL is required", "需要 git 遠端網址")
	}
	if len(raw) > maxGitURLLength {
		return gitTarget{}, locale.Errorf("the git remote URL is too long", "git 遠端網址太長")
	}
	if strings.ContainsFunc(raw, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return gitTarget{}, locale.Errorf("the git remote URL contains spaces or control characters", "git 遠端網址含有空白或控制字元")
	}
	if allowLocalGitURLs && (strings.HasPrefix(raw, "file://") || filepath.IsAbs(raw)) {
		return gitTarget{url: raw}, nil
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		// url.Parse quotes its input in the error, so it is not passed on.
		return gitTarget{}, locale.Errorf("the git remote URL is not a valid https:// URL", "git 遠端網址不是有效的 https:// 網址")
	}
	if !strings.EqualFold(parsed.Scheme, "https") {
		return gitTarget{}, locale.Errorf("the git remote must be an https:// URL (ssh, git://, http:// and file are not supported)", "git 遠端須為 https:// 網址（不支援 ssh、git://、http:// 與 file）")
	}
	if parsed.User != nil {
		return gitTarget{}, locale.Errorf("put the user name and token in their own fields, not in the URL", "請把使用者名稱與權杖填在各自的欄位，不要放在網址裡")
	}
	host := strings.ToLower(parsed.Hostname())
	if parsed.Opaque != "" || host == "" {
		return gitTarget{}, locale.Errorf("the git remote URL has no host", "git 遠端網址沒有主機")
	}
	if parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return gitTarget{}, locale.Errorf("the git remote URL must not have a query or a fragment", "git 遠端網址不能有查詢字串或片段")
	}
	port := parsed.Port()
	if port == "" {
		port = "443"
	} else if number, err := strconv.Atoi(port); err != nil || number < 1 || number > 65535 {
		return gitTarget{}, locale.Errorf("the git remote URL has an invalid port", "git 遠端網址的連接埠無效")
	}
	// The host git connects to and the one the pinned addresses are for
	// must be spelled the same.
	parsed.Scheme = "https"
	parsed.Host = strings.ToLower(parsed.Host)
	return gitTarget{url: parsed.String(), host: host, port: port}, nil
}

// validateGitUser keeps the user to something that fits in Basic auth: a
// colon would end it early.
func validateGitUser(user string) error {
	if len(user) > maxGitUserLength {
		return locale.Errorf("the git user name is too long", "git 使用者名稱太長")
	}
	if strings.ContainsRune(user, ':') || strings.ContainsFunc(user, unicode.IsControl) {
		return locale.Errorf("the git user name must not contain a colon or control characters", "git 使用者名稱不能含有冒號或控制字元")
	}
	return nil
}

func validateGitToken(token string) error {
	if len(token) > maxGitTokenLength {
		return locale.Errorf("the git token is too long", "git 權杖太長")
	}
	if strings.ContainsFunc(token, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return locale.Errorf("the git token must not contain spaces or control characters", "git 權杖不能含有空白或控制字元")
	}
	return nil
}

func gitUserOrDefault(user string) string {
	if user = strings.TrimSpace(user); user != "" {
		return user
	}
	return defaultGitUser
}

// allowPrivateGit follows notify's SMTP guard: only a loopback-only server
// may push to a private, loopback or link-local address. Once the server is
// reachable from the network, a user who can set the URL could otherwise use
// the push to probe or post to services behind the firewall.
func (m *RemoteManager) allowPrivateGit() bool {
	return m.pm == nil || !m.pm.IsRemote()
}

// resolveGitHost returns the addresses host may be reached at, refusing any
// the guard does not allow. It runs when the config is saved and again
// before every network command, because a DNS answer can change.
func resolveGitHost(ctx context.Context, host string, allowPrivate bool) ([]net.IP, error) {
	var ips []net.IP
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IP{ip}
	} else {
		lookupCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		found, err := lookupGitHost(lookupCtx, host)
		if err == nil && len(found) == 0 {
			err = locale.Errorf("no address", "沒有位址")
		}
		if err != nil {
			return nil, locale.Errorf("resolve git host %q: %w", "解析 git 主機「%s」：%w", host, err)
		}
		ips = found
	}
	for _, ip := range ips {
		if !allowPrivate && blockedGitIP(ip) {
			return nil, locale.Errorf("git host %q resolves to a private or local address", "git 主機「%s」解析到私有或本機位址", host)
		}
		if !ip.IsGlobalUnicast() && !(allowPrivate &&
			(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast())) {
			return nil, locale.Errorf("git host %q resolves to a non-unicast address", "git 主機「%s」解析到非單播位址", host)
		}
	}
	return ips, nil
}

func blockedGitIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || !ip.IsGlobalUnicast()
}

// CheckGitDestination validates cfg's URL and the addresses its host
// resolves to, as the save handler must before storing it.
func (m *RemoteManager) CheckGitDestination(ctx context.Context, cfg Config) error {
	target, err := parseGitURL(cfg.GitURL)
	if err != nil {
		return err
	}
	if target.host == "" {
		return nil
	}
	_, err = resolveGitHost(ctx, target.host, m.allowPrivateGit())
	return err
}

// ---------- the token ----------

// gitSecret is the encrypted file's content. Identity binds the token to the
// URL and user it was entered for, as notify binds the SMTP password.
type gitSecret struct {
	Token    string `json:"token"`
	Identity string `json:"identity"`
}

// gitTokenStore keeps the token beside the config in the app config
// directory, encrypted with the machine key: never in the workspace, which
// is the thing people sync and share.
func (m *RemoteManager) gitTokenStore() *secrets.Store {
	return secrets.New(
		strings.TrimSuffix(m.ConfigPath(), ".json")+".enc",
		filepath.Join(m.pm.CatalogRoot(), "secrets", "master.key"),
	)
}

func gitCredentialIdentity(cfg Config) string {
	target := strings.TrimSpace(cfg.GitURL)
	if parsed, err := parseGitURL(cfg.GitURL); err == nil {
		target = parsed.url
	}
	sum := sha256.Sum256([]byte(target + "\x00" + gitUserOrDefault(cfg.GitUser)))
	return hex.EncodeToString(sum[:])
}

// loadGitToken returns cfg's token, or "" when none is stored or the stored
// one was entered for another URL or user.
func (m *RemoteManager) loadGitToken(cfg Config) string {
	if m.pm == nil || cfg.Kind != "git" {
		return ""
	}
	data, err := m.gitTokenStore().Load()
	if err != nil {
		log.Printf("remote: cannot decrypt the git token: %v", err)
		return ""
	}
	if len(data) == 0 {
		return ""
	}
	var secret gitSecret
	if err := json.Unmarshal(data, &secret); err != nil {
		log.Printf("remote: ignoring a malformed encrypted git token")
		return ""
	}
	if secret.Identity != gitCredentialIdentity(cfg) {
		return ""
	}
	return secret.Token
}

func (m *RemoteManager) saveGitToken(cfg Config, token string) error {
	data, err := json.Marshal(gitSecret{Token: token, Identity: gitCredentialIdentity(cfg)})
	if err != nil {
		return err
	}
	return m.gitTokenStore().Save(data)
}

// HasGitToken reports whether a token is stored for cfg. The API returns
// this, never the token.
func (m *RemoteManager) HasGitToken(cfg Config) bool {
	return m.loadGitToken(cfg) != ""
}

// ---------- talking to the remote ----------

// gitSession is one run's way of reaching the remote: the checked target,
// the environment that carries the credentials, and what must never appear
// in an error.
type gitSession struct {
	target  gitTarget
	env     []string
	secrets []string
}

// openGitSession validates the config, resolves and checks the host now
// (not just when the config was saved), and prepares the credentials.
func (m *RemoteManager) openGitSession(ctx context.Context, cfg Config) (*gitSession, error) {
	if !gitrepo.Available() {
		return nil, gitrepo.ErrUnavailable
	}
	target, err := parseGitURL(cfg.GitURL)
	if err != nil {
		return nil, err
	}
	var pinned []net.IP
	if target.host != "" {
		pinned, err = resolveGitHost(ctx, target.host, m.allowPrivateGit())
		if err != nil {
			return nil, err
		}
	}
	return newGitSession(target, gitUserOrDefault(cfg.GitUser), m.loadGitToken(cfg), pinned), nil
}

func newGitSession(target gitTarget, user, token string, pinned []net.IP) *gitSession {
	settings := [][2]string{
		// The token below is the only credential; no helper from anywhere is
		// asked for another, or told to store this one.
		{"credential.helper", ""},
		// A redirect would carry the request, and the header with it, to a
		// host the address check never saw.
		{"http.followRedirects", "false"},
		// A fetch must not start housekeeping nobody asked for.
		{"gc.auto", "0"},
		{"maintenance.auto", "false"},
	}
	session := &gitSession{target: target}
	if token != "" {
		basic := base64.StdEncoding.EncodeToString([]byte(user + ":" + token))
		settings = append(settings, [2]string{"http.extraHeader", "Authorization: Basic " + basic})
		session.secrets = append(session.secrets, basic, token)
	}
	if target.host != "" && len(pinned) > 0 && net.ParseIP(target.host) == nil {
		// curl connects to the addresses just checked instead of asking DNS
		// again, so an answer that changes in between (DNS rebinding) cannot
		// point the push somewhere private. Git 2.37 or newer; older git
		// ignores it and relies on the check alone.
		addresses := make([]string, 0, len(pinned))
		for _, ip := range pinned {
			if ip.To4() == nil {
				addresses = append(addresses, "["+ip.String()+"]")
			} else {
				addresses = append(addresses, ip.String())
			}
		}
		settings = append(settings, [2]string{
			"http.curloptResolve", target.host + ":" + target.port + ":" + strings.Join(addresses, ","),
		})
	}
	env := []string{"GIT_CONFIG_COUNT=" + strconv.Itoa(len(settings))}
	for index, setting := range settings {
		env = append(env,
			fmt.Sprintf("GIT_CONFIG_KEY_%d=%s", index, setting[0]),
			fmt.Sprintf("GIT_CONFIG_VALUE_%d=%s", index, setting[1]),
		)
	}
	protocols := "https"
	if target.host == "" {
		protocols = "file"
	}
	env = append(env,
		"GIT_ALLOW_PROTOCOL="+protocols,
		// Config or tracing inherited from the server's environment could add
		// a helper, or print the Authorization header into an error. Tracing
		// is turned off and, should GIT_CURL_VERBOSE be inherited (git reads
		// it as on whatever its value, so it cannot be set to off here), git
		// is told to redact the header itself; redact below is the backstop.
		"GIT_CONFIG_PARAMETERS=",
		"GIT_TRACE_REDACT=1",
		"GIT_TRACE=0",
		"GIT_TRACE_CURL=0",
		"GIT_TRACE_PACKET=0",
		"GIT_TRACE2=0",
		"GIT_TRACE2_EVENT=0",
	)
	session.env = env
	return session
}

// run is a network command: the session's credentials go in through the
// environment, and its error comes back without them.
func (s *gitSession) run(ctx context.Context, repo *gitrepo.Repo, args ...string) ([]byte, error) {
	out, err := runGit(ctx, repo, s.env, args...)
	if err != nil {
		return out, s.redact(err)
	}
	return out, nil
}

var (
	authorizationText = regexp.MustCompile(`(?i)(authorization:\s*)[^\r\n]*`)
	urlUserinfo       = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)[^/@\s]*@`)
)

// redact turns a git error into one that is safe to log and return: no token,
// no Authorization header, no user:password@ in a URL.
func (s *gitSession) redact(err error) error {
	message := err.Error()
	for _, secret := range s.secrets {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "[redacted]")
		}
	}
	return errors.New(redactGitText(message))
}

func redactGitText(text string) string {
	text = authorizationText.ReplaceAllString(text, "${1}[redacted]")
	return urlUserinfo.ReplaceAllString(text, "${1}[redacted]@")
}

// remoteCommit asks the remote where ref points; "" means it does not exist.
func (s *gitSession) remoteCommit(ctx context.Context, repo *gitrepo.Repo, ref string) (string, error) {
	out, err := s.run(ctx, repo, "ls-remote", s.target.url, ref)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == ref && gitrepo.ValidSHA(fields[0]) {
			return fields[0], nil
		}
	}
	return "", nil
}

// compare classifies a project's branch against its local HEAD. The remote
// commit has to be in the local repository to compare, so it is fetched
// into a private ref first unless it is already there (it usually is: we
// pushed it).
func (s *gitSession) compare(ctx context.Context, p gitProject, remote string) (string, SyncState, error) {
	if remote == "" {
		return "", SyncRemoteMiss, nil
	}
	if remote == p.head {
		return remote, SyncSynced, nil
	}
	if _, err := runGit(ctx, p.repo, nil, "cat-file", "-e", remote+"^{commit}"); err != nil {
		tracking := gitTrackingPrefix + strings.TrimPrefix(p.branch, gitBranchPrefix)
		if _, err := s.run(ctx, p.repo, "fetch", "--quiet", "--no-tags", "--no-write-fetch-head",
			s.target.url, "+refs/heads/"+p.branch+":"+tracking); err != nil {
			return remote, "", err
		}
		// The branch may have moved again since ls-remote; what was fetched
		// is what gets compared.
		out, err := runGit(ctx, p.repo, nil, "rev-parse", "--verify", "--quiet", tracking+"^{commit}")
		if err != nil {
			return remote, "", locale.Errorf("read fetched branch %s: %w", "讀取抓取的分支 %s：%w", p.branch, err)
		}
		remote = strings.TrimSpace(string(out))
		if remote == p.head {
			return remote, SyncSynced, nil
		}
	}
	behind, err := isAncestor(ctx, p.repo, remote, p.head)
	if err != nil {
		return remote, "", err
	}
	if behind {
		return remote, SyncLocalNewer, nil
	}
	ahead, err := isAncestor(ctx, p.repo, p.head, remote)
	if err != nil {
		return remote, "", err
	}
	if ahead {
		return remote, SyncRemoteNewer, nil
	}
	return remote, SyncConflict, nil
}

// isAncestor is merge-base --is-ancestor, which answers with its exit code:
// 0 yes, 1 no, anything else an error.
func isAncestor(ctx context.Context, repo *gitrepo.Repo, ancestor, descendant string) (bool, error) {
	_, err := runGit(ctx, repo, nil, "merge-base", "--is-ancestor", ancestor, descendant)
	if err == nil {
		return true, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

// push sends commit to refs/heads/<branch>. Never forced: if the remote
// moved since it was checked, git refuses and the push is reported failed.
func (s *gitSession) push(ctx context.Context, repo *gitrepo.Repo, commit, branch string) error {
	_, err := s.run(ctx, repo, "push", "--quiet", s.target.url, commit+":refs/heads/"+branch)
	return err
}

// ---------- projects ----------

// gitProject is one project whose repository has a commit to push.
type gitProject struct {
	name   string
	branch string
	repo   *gitrepo.Repo
	head   string
}

var refUnsafe = regexp.MustCompile(`[^\p{L}\p{N}_.-]+`)

// gitBranchComponent turns a project name into one ref component. Nested
// projects ("a/b") are flattened to "a--b": "nodevas/a" and "nodevas/a/b"
// cannot both exist as branches. The workspace's own project (".") is named
// after the workspace folder.
func gitBranchComponent(project, workspaceLabel string) string {
	name := project
	if name == "." {
		name = workspaceLabel
	}
	parts := strings.Split(name, "/")
	for index, part := range parts {
		parts[index] = refUnsafe.ReplaceAllString(part, "-")
	}
	component := strings.Join(parts, "--")
	for strings.Contains(component, "..") {
		component = strings.ReplaceAll(component, "..", ".")
	}
	component = strings.Trim(component, ".-")
	for strings.HasSuffix(component, ".lock") {
		component = strings.Trim(strings.TrimSuffix(component, ".lock"), ".-")
	}
	if runes := []rune(component); len(runes) > maxGitRefLength {
		component = strings.Trim(string(runes[:maxGitRefLength]), ".-")
	}
	if component == "" {
		component = "project"
	}
	return component
}

// gitProjects lists the projects with a repository and a commit. A project
// with neither is not an error, just nothing to push yet; it is not opened
// either, since gitrepo.Open would create a repository. A project whose
// branch name another project already took comes back as a status with an
// error, so it is visible rather than silently overwriting the other.
func (m *RemoteManager) gitProjects(ctx context.Context) ([]gitProject, []GitProjectStatus, error) {
	if m.pm == nil {
		return nil, nil, locale.Errorf("project manager is unavailable", "無法使用專案管理器")
	}
	infos, err := m.pm.List()
	if err != nil {
		return nil, nil, err
	}
	label := filepath.Base(m.pm.Workspace())
	taken := map[string]string{}
	var projects []gitProject
	var clashes []GitProjectStatus
	for _, info := range infos {
		if info.IsFolder {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		project, ok, err := openGitProject(ctx, info.Name, info.Path, label)
		if err != nil {
			clashes = append(clashes, GitProjectStatus{Project: info.Name}.failed(err))
			continue
		}
		if !ok {
			continue
		}
		if owner, clash := taken[project.branch]; clash {
			clashes = append(clashes, GitProjectStatus{Project: info.Name, Branch: project.branch}.failed(locale.Errorf(
				"branch %s is already used by project %q; rename one of them",
				"分支 %s 已被專案「%s」使用；請將其中一個改名", project.branch, owner)))
			continue
		}
		taken[project.branch] = info.Name
		projects = append(projects, project)
	}
	return projects, clashes, nil
}

func openGitProject(ctx context.Context, name, root, workspaceLabel string) (gitProject, bool, error) {
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(gitrepo.Dir), "HEAD")); err != nil {
		return gitProject{}, false, nil
	}
	repo, err := gitrepo.Open(ctx, root)
	if err != nil {
		return gitProject{}, false, locale.Errorf("open history of %s: %w", "開啟 %s 的歷史紀錄：%w", name, err)
	}
	out, err := runGit(ctx, repo, nil, "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
	if err != nil {
		return gitProject{}, false, nil
	}
	return gitProject{
		name:   name,
		branch: gitBranchPrefix + gitBranchComponent(name, workspaceLabel),
		repo:   repo,
		head:   strings.TrimSpace(string(out)),
	}, true, nil
}

// ---------- status and push ----------

// GitProjectStatus is one project's branch on the git remote. It holds
// commit IDs and messages, never credentials, so the UI may have it.
type GitProjectStatus struct {
	Project      string    `json:"project"`
	Branch       string    `json:"branch,omitempty"`
	State        SyncState `json:"state,omitempty"`
	LocalCommit  string    `json:"localCommit,omitempty"`
	RemoteCommit string    `json:"remoteCommit,omitempty"`
	// Error is why the state could not be worked out.
	Error    string         `json:"error,omitempty"`
	LastPush *GitPushResult `json:"lastPush,omitempty"`
	// err is Error as the error it was, worded for any reader.
	err error
}

// failed is s that could not be worked out because of err.
func (s GitProjectStatus) failed(err error) GitProjectStatus {
	s.Error, s.err = err.Error(), err
	return s
}

// failure is why s could not be worked out, or nil.
func (s GitProjectStatus) failure() error {
	if s.err == nil && s.Error != "" {
		return locale.Errorf("%s", "%s", s.Error)
	}
	return s.err
}

// GitPushResult is how the last push of a project went.
type GitPushResult struct {
	At     time.Time `json:"at"`
	Branch string    `json:"branch"`
	Commit string    `json:"commit,omitempty"`
	OK     bool      `json:"ok"`
	Error  string    `json:"error,omitempty"`
}

// aggregateGitSync folds the projects into the one state the sync banner
// shows. The worst wins; a branch missing on the remote reads as "no
// snapshot yet" only when every branch is missing, and otherwise as local
// changes not yet pushed. Projects whose state is unknown do not count.
func aggregateGitSync(projects []GitProjectStatus) SyncState {
	seen := map[SyncState]bool{}
	known, missing := 0, 0
	for _, project := range projects {
		if project.State == "" {
			continue
		}
		known++
		seen[project.State] = true
		if project.State == SyncRemoteMiss {
			missing++
		}
	}
	switch {
	case seen[SyncConflict]:
		return SyncConflict
	case seen[SyncRemoteNewer]:
		return SyncRemoteNewer
	case seen[SyncLocalNewer]:
		return SyncLocalNewer
	case missing > 0 && missing == known:
		return SyncRemoteMiss
	case missing > 0:
		return SyncLocalNewer
	default:
		return SyncSynced
	}
}

// gitRun is what one pass over the projects found and did.
type gitRun struct {
	status SyncStatus
	pushed int
	// held are the projects not pushed because their branch moved on.
	held   []string
	failed []error
}

// runGitBackup works out every project's state and, with push, pushes those
// that are only ahead of their branch (or have none yet). A diverged
// project holds back only itself. only limits the pass to one project.
// Callers hold backupMu.
func (m *RemoteManager) runGitBackup(ctx context.Context, cfg Config, push bool, only string) (gitRun, error) {
	state := m.loadState()
	run := gitRun{status: SyncStatus{State: SyncSynced, LastBackupAt: state.LastBackupAt}}
	session, err := m.openGitSession(ctx, cfg)
	if err != nil {
		return run, err
	}
	projects, clashes, err := m.gitProjects(ctx)
	if err != nil {
		return run, err
	}
	if only != "" {
		projects, clashes, err = onlyProject(projects, clashes, only)
		if err != nil {
			return run, err
		}
	}

	pushes := map[string]GitPushResult{}
	statuses := make([]GitProjectStatus, 0, len(projects)+len(clashes))
	checked := 0
	var firstErr error
	for _, project := range projects {
		status := GitProjectStatus{Project: project.name, Branch: project.branch, LocalCommit: project.head}
		remote, err := session.remoteCommit(ctx, project.repo, "refs/heads/"+project.branch)
		var sync SyncState
		if err == nil {
			remote, sync, err = session.compare(ctx, project, remote)
		}
		status.RemoteCommit = remote
		if err != nil {
			status = status.failed(err)
			if firstErr == nil {
				firstErr = err
			}
			statuses = append(statuses, status)
			continue
		}
		checked++
		status.State = sync
		if push {
			switch sync {
			case SyncLocalNewer, SyncRemoteMiss:
				result := GitPushResult{At: time.Now().UTC(), Branch: project.branch, Commit: project.head}
				if err := session.push(ctx, project.repo, project.head, project.branch); err != nil {
					result.Error = err.Error()
					run.failed = append(run.failed, fmt.Errorf("%s: %w", project.name, err))
				} else {
					result.OK = true
					status.State = SyncSynced
					status.RemoteCommit = project.head
					run.pushed++
				}
				pushes[project.name] = result
			case SyncRemoteNewer, SyncConflict:
				run.held = append(run.held, project.name)
			}
		}
		statuses = append(statuses, status)
	}
	statuses = append(statuses, clashes...)
	if len(projects) > 0 && checked == 0 {
		return run, firstErr
	}

	if push {
		state = m.recordGitPushes(pushes, len(run.failed) == 0 && only == "")
		run.status.LastBackupAt = state.LastBackupAt
	}
	for index := range statuses {
		if result, ok := state.GitPushes[statuses[index].Project]; ok {
			result := result
			statuses[index].LastPush = &result
		}
	}
	run.status.Projects = statuses
	run.status.State = aggregateGitSync(statuses)
	return run, nil
}

func onlyProject(projects []gitProject, clashes []GitProjectStatus, name string) ([]gitProject, []GitProjectStatus, error) {
	for _, project := range projects {
		if project.name == name {
			return []gitProject{project}, nil, nil
		}
	}
	for _, clash := range clashes {
		if clash.Project == name {
			return nil, nil, clash.failure()
		}
	}
	return nil, nil, locale.Errorf("project %q: %w", "專案「%s」：%w", name, ErrNoGitHistory)
}

// recordGitPushes stores the push results. A full pass with no failure also
// counts as the scheduled backup having run, including when everything was
// already up to date, so the schedule waits a whole interval again rather
// than asking the remote every quarter hour.
func (m *RemoteManager) recordGitPushes(pushes map[string]GitPushResult, completed bool) remoteState {
	state := m.loadState()
	if len(pushes) == 0 && !completed {
		return state
	}
	if state.GitPushes == nil {
		state.GitPushes = map[string]GitPushResult{}
	}
	for project, result := range pushes {
		state.GitPushes[project] = result
	}
	if completed {
		state.LastBackupAt = time.Now()
		state.LastPushedBackend = "git"
	}
	if err := m.saveState(state); err != nil {
		log.Printf("remote: could not record git push results: %v", err)
	}
	return state
}

// gitFlush is FlushBackup for the git backend. It pushes every project it
// safely can. It refuses with ErrSyncConflict only when a project was held
// back and nothing else was pushed; when some were pushed, the held ones
// are in the status (and the sync banner) rather than failing the whole run.
func (m *RemoteManager) gitFlush(ctx context.Context, cfg Config) (RemoteRef, error) {
	run, err := m.runGitBackup(ctx, cfg, true, "")
	if err != nil {
		return RemoteRef{}, err
	}
	if len(run.failed) > 0 {
		return RemoteRef{}, errors.Join(run.failed...)
	}
	if len(run.status.Projects) == 0 {
		return RemoteRef{}, ErrNoGitHistory
	}
	if run.pushed == 0 && len(run.held) > 0 {
		return RemoteRef{}, fmt.Errorf("%w: %s", ErrSyncConflict, strings.Join(run.held, ", "))
	}
	return RemoteRef{
		ID:       gitBranchPrefix + "*",
		Name:     gitBranchPrefix + "*",
		Modified: time.Now(),
	}, nil
}

// maybeGitBackup is the scheduled pass for the git backend.
func (m *RemoteManager) maybeGitBackup(ctx context.Context, cfg Config) {
	run, err := m.runGitBackup(ctx, cfg, true, "")
	if err != nil {
		log.Printf("scheduled backup: %v", err)
		return
	}
	if run.pushed > 0 {
		log.Printf("scheduled backup pushed %d project(s) to git", run.pushed)
	}
	for _, failure := range run.failed {
		log.Printf("scheduled backup failed: %v", failure)
	}
	if len(run.held) > 0 {
		log.Printf("scheduled backup paused for %s: the remote branch has commits this machine does not",
			strings.Join(run.held, ", "))
	}
}

// PushGitProject pushes one project's branch, the git backend's "back up
// now". It refuses with ErrSyncConflict when the branch moved on the remote.
func (m *RemoteManager) PushGitProject(ctx context.Context, project string) (GitProjectStatus, error) {
	if err := m.acquireBackup(ctx); err != nil {
		return GitProjectStatus{}, err
	}
	defer m.releaseBackup()
	run, err := m.runGitBackup(ctx, m.Load(), true, project)
	if err != nil {
		return GitProjectStatus{}, err
	}
	if len(run.status.Projects) == 0 {
		return GitProjectStatus{}, ErrNoGitHistory
	}
	status := run.status.Projects[0]
	if len(run.failed) > 0 {
		return status, run.failed[0]
	}
	if err := status.failure(); err != nil {
		return status, err
	}
	if len(run.held) > 0 {
		return status, ErrSyncConflict
	}
	return status, nil
}

// KeepLocalGit is "keep local" for a project whose branch diverged: its
// HEAD goes to a new side branch, nodevas/<project>-local-<time>, so the
// work is backed up without touching what is on the remote branch. It
// returns the side branch's name.
func (m *RemoteManager) KeepLocalGit(ctx context.Context, project string) (string, error) {
	if err := m.acquireBackup(ctx); err != nil {
		return "", err
	}
	defer m.releaseBackup()
	cfg := m.Load()
	if cfg.Kind != "git" {
		return "", locale.Errorf("the backup is not a git remote", "這個備份不是 git 遠端")
	}
	session, err := m.openGitSession(ctx, cfg)
	if err != nil {
		return "", err
	}
	projects, clashes, err := m.gitProjects(ctx)
	if err != nil {
		return "", err
	}
	projects, _, err = onlyProject(projects, clashes, project)
	if err != nil {
		return "", err
	}
	target := projects[0]
	branch := target.branch + "-local-" + time.Now().UTC().Format("20060102-150405")
	if err := session.push(ctx, target.repo, target.head, branch); err != nil {
		return "", err
	}
	log.Printf("remote: kept local history of %s on %s", target.name, branch)
	return branch, nil
}

// TestGitConnection asks the saved remote for its branches (ls-remote) with
// the saved credentials, and returns how many of them are Nodevas's.
func (m *RemoteManager) TestGitConnection(ctx context.Context) (int, error) {
	cfg := m.Load()
	if cfg.Kind != "git" {
		return 0, locale.Errorf("the backup is not a git remote", "這個備份不是 git 遠端")
	}
	session, err := m.openGitSession(ctx, cfg)
	if err != nil {
		return 0, err
	}
	// ls-remote needs a repository to run in, and there may be no project
	// with one yet; an empty scratch repository does.
	scratch, err := os.MkdirTemp("", "nodevas-remote-test-")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(scratch)
	repo, err := gitrepo.Open(ctx, scratch)
	if err != nil {
		return 0, err
	}
	out, err := session.run(ctx, repo, "ls-remote", "--heads", session.target.url)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.HasPrefix(fields[1], "refs/heads/"+gitBranchPrefix) {
			count++
		}
	}
	return count, nil
}
