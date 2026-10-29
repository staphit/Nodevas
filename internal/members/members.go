// Package members decides who may reach a project on a server with accounts.
//
// A project with no members is open to every signed-in account, which is how
// every server behaved before membership existed and how a personal server
// stays. Once a project has members, only they and server administrators may
// reach it, each as their role allows:
//
//	viewer  reads the board, its runs and its agent roster; writes nothing
//	editor  reads and writes: the board, runs, agent messages
//	owner   an editor who also manages the member list
//
// Administrators reach every project as owners. Agents act as the account
// they run for (an MCP session's user, a runner's owner), so they get that
// account's role; an agent never manages members.
//
// The table is small (people times projects), so the store keeps all of it in
// memory and answers access checks without a query: the check runs on every
// project-scoped request and on every realtime event fanned out to a client.
package members

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"nodevas/internal/db"
	"nodevas/internal/identity"
	"nodevas/internal/locale"
)

// Role is a member's role in one project.
type Role string

const (
	RoleOwner  Role = "owner"
	RoleEditor Role = "editor"
	RoleViewer Role = "viewer"
)

// ParseRole accepts a role name as the API takes it.
func ParseRole(s string) (Role, bool) {
	switch Role(strings.ToLower(strings.TrimSpace(s))) {
	case RoleOwner:
		return RoleOwner, true
	case RoleEditor:
		return RoleEditor, true
	case RoleViewer:
		return RoleViewer, true
	}
	return "", false
}

var (
	// ErrInvalid is a request the caller got wrong (400).
	ErrInvalid = locale.Errorf("invalid member request", "成員請求無效")
	// ErrForbidden is a caller who may not manage this project's members (403).
	ErrForbidden   = locale.Errorf("only the project's owners and server administrators may manage its members", "只有專案擁有者與伺服器管理員可以管理成員")
	errUnavailable = locale.Errorf("project members could not be loaded; see the server log", "無法載入專案成員；請查看伺服器紀錄")
	// ErrLastOwner refuses to leave a restricted project without an owner.
	ErrLastOwner = locale.Errorf("a project with members needs at least one owner; add another owner first, or open the project to everyone", "有成員的專案至少需要一位擁有者；請先加入另一位擁有者，或把專案開放給所有人")
)

// MaxMembers bounds one project's member list.
const MaxMembers = 500

// Access is what one actor may do in one project.
type Access struct {
	// Restricted is true when the project has members at all.
	Restricted bool
	// Role is the actor's own role, "" when they are not a member.
	Role Role
	// Admin is a server administrator, who reaches every project.
	Admin bool
}

// CanRead reports whether the actor may see the project at all.
func (a Access) CanRead() bool { return !a.Restricted || a.Admin || a.Role != "" }

// CanWrite reports whether the actor may change the project.
func (a Access) CanWrite() bool {
	return !a.Restricted || a.Admin || a.Role == RoleOwner || a.Role == RoleEditor
}

// EffectiveRole is the role the UI shows: an open project makes everyone an
// editor, an administrator is an owner everywhere.
func (a Access) EffectiveRole() Role {
	switch {
	case a.Admin:
		return RoleOwner
	case !a.Restricted:
		return RoleEditor
	}
	return a.Role
}

// Member is one row of a project's member list.
type Member struct {
	UserID    string `json:"userId"`
	Name      string `json:"name"`
	Role      Role   `json:"role"`
	AddedBy   string `json:"addedBy,omitempty"`
	CreatedAt string `json:"createdAt"`
}

// Store is the membership table and its in-memory copy.
type Store struct {
	database *db.DB
	clock    func() time.Time

	mu sync.RWMutex
	// roles maps a project key to its members' roles by user id. A project
	// with no entry is open.
	roles  map[string]map[string]Role
	loaded bool
	// onChange is told which project's members changed, so the realtime hub
	// can drop subscribers who lost access.
	onChange func(project string)
	// closed is a store whose table could not be read: every project counts
	// as restricted with no members, so only administrators get in.
	closed bool
}

// Unavailable is the store to serve with when the table cannot be read. It
// fails closed: a restricted project must not open up because a query
// failed, so every project is reachable by administrators only.
func Unavailable() *Store {
	return &Store{clock: time.Now, roles: map[string]map[string]Role{}, closed: true}
}

// NewStore wraps the workspace database. Load it before serving.
func NewStore(database *db.DB) *Store {
	return &Store{database: database, clock: time.Now, roles: map[string]map[string]Role{}}
}

// OnChange registers the callback told about every member-list change.
func (s *Store) OnChange(fn func(project string)) {
	s.mu.Lock()
	s.onChange = fn
	s.mu.Unlock()
}

// Load reads the whole table into memory.
func (s *Store) Load(ctx context.Context) error {
	if s == nil || s.database == nil {
		return nil
	}
	rows, err := s.database.QueryContext(ctx, `SELECT project, user_id, role FROM project_members`)
	if err != nil {
		return locale.Errorf("read project members: %w", "讀取專案成員：%w", err)
	}
	defer rows.Close()
	roles := map[string]map[string]Role{}
	for rows.Next() {
		var project, user, role string
		if err := rows.Scan(&project, &user, &role); err != nil {
			return err
		}
		parsed, ok := ParseRole(role)
		if !ok {
			// The column is constrained, but a hand-edited row must fail
			// closed: it still restricts the project, and grants nothing.
			parsed = ""
		}
		if roles[project] == nil {
			roles[project] = map[string]Role{}
		}
		roles[project][user] = parsed
	}
	if err := rows.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	s.roles = roles
	s.loaded = true
	s.mu.Unlock()
	return nil
}

// Access answers for actor in the project keyed project (project.Key).
func (s *Store) Access(actor identity.Actor, project string) Access {
	access := Access{Admin: actor.IsAdmin()}
	if s == nil {
		return access
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		access.Restricted = true
		return access
	}
	members, restricted := s.roles[project]
	if !restricted || len(members) == 0 {
		return access
	}
	access.Restricted = true
	if actor.ID != "" {
		access.Role = members[actor.ID]
	}
	return access
}

// Restricted reports whether the project has members.
func (s *Store) Restricted(project string) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.closed || len(s.roles[project]) > 0
}

// List returns a project's members, owners first, then by name.
func (s *Store) List(ctx context.Context, project string) ([]Member, error) {
	if s.database == nil {
		return nil, errUnavailable
	}
	rows, err := s.database.QueryContext(ctx, `
SELECT m.user_id, COALESCE(a.name, ''), m.role, m.added_by, m.created_at
FROM project_members m LEFT JOIN accounts a ON a.id = m.user_id
WHERE m.project = ?`, project)
	if err != nil {
		return nil, locale.Errorf("read project members: %w", "讀取專案成員：%w", err)
	}
	defer rows.Close()
	out := []Member{}
	for rows.Next() {
		var m Member
		var role string
		if err := rows.Scan(&m.UserID, &m.Name, &role, &m.AddedBy, &m.CreatedAt); err != nil {
			return nil, err
		}
		m.Role = Role(role)
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rank := map[Role]int{RoleOwner: 0, RoleEditor: 1, RoleViewer: 2}
	sort.SliceStable(out, func(i, j int) bool {
		if rank[out[i].Role] != rank[out[j].Role] {
			return rank[out[i].Role] < rank[out[j].Role]
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// CanManage reports whether actor may change the project's member list: a
// server administrator, an owner, or, while the project is still open,
// any signed-in person (who becomes its first owner). Agents never may.
func (s *Store) CanManage(actor identity.Actor, project string) bool {
	if actor.IsAgent() || actor.ID == "" || s.database == nil {
		return false
	}
	access := s.Access(actor, project)
	return access.Admin || !access.Restricted || access.Role == RoleOwner
}

// Set adds a member or changes their role. On an open project the caller
// becomes an owner too (unless they are the one being set), so restricting a
// project never locks out the person who did it.
func (s *Store) Set(ctx context.Context, actor identity.Actor, project, userID string, role Role) error {
	project, userID = strings.TrimSpace(project), strings.TrimSpace(userID)
	if project == "" || userID == "" {
		return locale.Errorf("%w: project and user are required", "%w：需要提供專案與使用者", ErrInvalid)
	}
	if _, ok := ParseRole(string(role)); !ok {
		return locale.Errorf("%w: role must be owner, editor or viewer", "%w：角色須為 owner、editor 或 viewer", ErrInvalid)
	}
	if !s.CanManage(actor, project) {
		return ErrForbidden
	}
	s.mu.RLock()
	current := cloneRoles(s.roles[project])
	s.mu.RUnlock()
	if _, exists := current[userID]; !exists && len(current) >= MaxMembers {
		return locale.Errorf("%w: a project has at most %d members", "%w：一個專案最多 %d 位成員", ErrInvalid, MaxMembers)
	}
	next := cloneRoles(current)
	next[userID] = role
	if len(current) == 0 && userID != actor.ID {
		next[actor.ID] = RoleOwner
	}
	if !hasOwner(next) {
		return ErrLastOwner
	}
	now := s.clock().UTC().Format(time.RFC3339Nano)
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for user, r := range next {
		if prev, had := current[user]; had && prev == r {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO project_members (project, user_id, role, added_by, created_at) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (project, user_id) DO UPDATE SET role = excluded.role`,
			project, user, string(r), actor.ID, now); err != nil {
			return locale.Errorf("write project member: %w", "寫入專案成員：%w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.replace(project, next)
	return nil
}

// Remove takes a member off the list. The last owner cannot be removed while
// others remain: the project would be restricted with nobody to manage it.
// Removing the last member of all reopens the project; Open says so plainly.
func (s *Store) Remove(ctx context.Context, actor identity.Actor, project, userID string) error {
	if !s.CanManage(actor, project) {
		return ErrForbidden
	}
	s.mu.RLock()
	current := cloneRoles(s.roles[project])
	s.mu.RUnlock()
	if _, ok := current[userID]; !ok {
		return nil
	}
	next := cloneRoles(current)
	delete(next, userID)
	if len(next) > 0 && !hasOwner(next) {
		return ErrLastOwner
	}
	if len(next) == 0 {
		return s.Open(ctx, actor, project)
	}
	if _, err := s.database.ExecContext(ctx,
		`DELETE FROM project_members WHERE project = ? AND user_id = ?`, project, userID); err != nil {
		return locale.Errorf("remove project member: %w", "移除專案成員：%w", err)
	}
	s.replace(project, next)
	return nil
}

// Open removes every member, opening the project to every signed-in account.
func (s *Store) Open(ctx context.Context, actor identity.Actor, project string) error {
	if !s.CanManage(actor, project) {
		return ErrForbidden
	}
	if _, err := s.database.ExecContext(ctx, `DELETE FROM project_members WHERE project = ?`, project); err != nil {
		return locale.Errorf("open project: %w", "開啟專案：%w", err)
	}
	s.replace(project, nil)
	return nil
}

// Moved follows a project directory (and every project under it) to its new
// place, so a rename never leaves its members behind and its content open.
func (s *Store) Moved(ctx context.Context, from, to string) error {
	if s == nil || from == "" || to == "" || from == to {
		return nil
	}
	moves := s.under(from)
	if len(moves) == 0 {
		return nil
	}
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	renamed := map[string]string{}
	for _, old := range moves {
		next := to + strings.TrimPrefix(old, from)
		renamed[old] = next
		if _, err := tx.ExecContext(ctx, `DELETE FROM project_members WHERE project = ?`, next); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE project_members SET project = ? WHERE project = ?`, next, old); err != nil {
			return locale.Errorf("move project members: %w", "搬移專案成員：%w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.mu.Lock()
	var changed []string
	for old, next := range renamed {
		s.roles[next] = s.roles[old]
		delete(s.roles, old)
		changed = append(changed, old, next)
	}
	notify := s.onChange
	s.mu.Unlock()
	for _, p := range changed {
		if notify != nil {
			notify(p)
		}
	}
	return nil
}

// Copied gives a copy of a project (and of every project under it) the
// original's members: a copy must not be a way to read a restricted project
// with the restriction left behind.
func (s *Store) Copied(ctx context.Context, from, to string) error {
	if s == nil || from == "" || to == "" || from == to {
		return nil
	}
	for _, old := range s.under(from) {
		next := to + strings.TrimPrefix(old, from)
		s.mu.RLock()
		roles := cloneRoles(s.roles[old])
		s.mu.RUnlock()
		now := s.clock().UTC().Format(time.RFC3339Nano)
		for user, role := range roles {
			if _, err := s.database.ExecContext(ctx, `
INSERT INTO project_members (project, user_id, role, added_by, created_at) VALUES (?, ?, ?, '', ?)
ON CONFLICT (project, user_id) DO UPDATE SET role = excluded.role`, next, user, string(role), now); err != nil {
				return locale.Errorf("copy project members: %w", "複製專案成員：%w", err)
			}
		}
		s.mu.Lock()
		merged := cloneRoles(s.roles[next])
		for user, role := range roles {
			merged[user] = role
		}
		s.roles[next] = merged
		s.mu.Unlock()
	}
	return nil
}

// Removed forgets the members of a deleted project and everything under it,
// so a new project created at the same place starts open, not inheriting.
func (s *Store) Removed(ctx context.Context, dir string) error {
	if s == nil || dir == "" {
		return nil
	}
	for _, p := range s.under(dir) {
		if _, err := s.database.ExecContext(ctx, `DELETE FROM project_members WHERE project = ?`, p); err != nil {
			return locale.Errorf("forget project members: %w", "清除專案成員：%w", err)
		}
		s.replace(p, nil)
	}
	return nil
}

// RestrictedUnder lists the restricted project keys at dir or below it.
func (s *Store) RestrictedUnder(dir string) []string { return s.under(dir) }

// under lists the restricted project keys at dir or below it.
func (s *Store) under(dir string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []string
	for p := range s.roles {
		if p == dir || strings.HasPrefix(p, dir+"/") || strings.HasPrefix(p, dir+`\`) {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

func (s *Store) replace(project string, roles map[string]Role) {
	s.mu.Lock()
	if len(roles) == 0 {
		delete(s.roles, project)
	} else {
		s.roles[project] = roles
	}
	notify := s.onChange
	s.mu.Unlock()
	if notify != nil {
		notify(project)
	}
}

func cloneRoles(in map[string]Role) map[string]Role {
	out := make(map[string]Role, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	return out
}

func hasOwner(roles map[string]Role) bool {
	for _, r := range roles {
		if r == RoleOwner {
			return true
		}
	}
	return false
}
