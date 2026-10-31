package agents

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Attachment limits. Files ride on a message as small UTF-8 text documents (a
// log excerpt, a patch, a config): enough to hand a teammate context, small
// enough that a broadcast copying them to every recipient stays cheap.
const (
	MaxFiles       = 10
	MaxFileBytes   = 256 << 10
	MaxFileNameLen = 128
)

// FileInput is one attachment as a sender hands it over.
type FileInput struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

// FileInfo describes a stored attachment without its body.
type FileInfo struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type storedFile struct {
	FileInfo
	content string
}

// ValidateFileName reports whether name may name an attachment: a base name
// only, so a recipient saving it cannot be steered into another folder, and
// no leading dot, so it cannot land as a dotfile (.bashrc, .env, .git*) that
// some tool would act on.
func ValidateFileName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("%w: a file needs a name", ErrInvalid)
	case !utf8.ValidString(name):
		return fmt.Errorf("%w: file name %q is not UTF-8", ErrInvalid, name)
	case utf8.RuneCountInString(name) > MaxFileNameLen:
		return fmt.Errorf("%w: file name %q is longer than %d characters", ErrInvalid, name, MaxFileNameLen)
	case strings.ContainsAny(name, `/\`):
		return fmt.Errorf("%w: file name %q must be a base name, without a path", ErrInvalid, name)
	case strings.HasPrefix(name, "."):
		return fmt.Errorf("%w: file name %q must not start with a dot", ErrInvalid, name)
	case strings.TrimSpace(name) == "":
		return fmt.Errorf("%w: a file needs a name", ErrInvalid)
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return fmt.Errorf("%w: file name %q contains a control character", ErrInvalid, name)
		}
	}
	return nil
}

// ValidateFileContent reports whether content may be an attachment: UTF-8
// text of at most MaxFileBytes. NUL is refused too: it is not text, and
// PostgreSQL cannot store it in a TEXT column.
func ValidateFileContent(name, content string) error {
	switch {
	case len(content) > MaxFileBytes:
		return fmt.Errorf("%w: file %q is larger than %d bytes", ErrInvalid, name, MaxFileBytes)
	case !utf8.ValidString(content) || strings.ContainsRune(content, 0):
		return fmt.Errorf("%w: file %q is not UTF-8 text", ErrInvalid, name)
	}
	return nil
}

func prepareFiles(in []FileInput) ([]storedFile, error) {
	if len(in) > MaxFiles {
		return nil, fmt.Errorf("%w: at most %d files per message", ErrInvalid, MaxFiles)
	}
	out := make([]storedFile, 0, len(in))
	for _, f := range in {
		if err := ValidateFileName(f.Name); err != nil {
			return nil, err
		}
		if err := ValidateFileContent(f.Name, f.Content); err != nil {
			return nil, err
		}
		// Two names that differ only in case would overwrite each other on a
		// recipient's Windows or macOS disk.
		for _, prev := range out {
			if strings.EqualFold(prev.Name, f.Name) {
				return nil, fmt.Errorf("%w: file %q is attached twice", ErrInvalid, f.Name)
			}
		}
		sum := sha256.Sum256([]byte(f.Content))
		out = append(out, storedFile{
			FileInfo: FileInfo{Name: f.Name, Size: int64(len(f.Content)), SHA256: hex.EncodeToString(sum[:])},
			content:  f.Content,
		})
	}
	return out, nil
}

func fileInfos(files []storedFile) []FileInfo {
	if len(files) == 0 {
		return nil
	}
	out := make([]FileInfo, len(files))
	for i, f := range files {
		out[i] = f.FileInfo
	}
	return out
}

// attachFileInfos fills Files on msgs from agent_message_files.
func (h *Hub) attachFileInfos(ctx context.Context, msgs []Message) error {
	if h.database == nil || len(msgs) == 0 {
		return nil
	}
	index := make(map[string]int, len(msgs))
	args := make([]any, 0, len(msgs))
	for i, m := range msgs {
		index[m.ID] = i
		args = append(args, m.ID)
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(args)), ", ")
	rows, err := h.database.QueryContext(ctx, `
		SELECT message_id, name, size, sha256
		  FROM agent_message_files
		 WHERE message_id IN (`+placeholders+`)
		 ORDER BY message_id, name
	`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var f FileInfo
		if err := rows.Scan(&id, &f.Name, &f.Size, &f.SHA256); err != nil {
			return err
		}
		if i, ok := index[id]; ok {
			msgs[i].Files = append(msgs[i].Files, f)
		}
	}
	return rows.Err()
}

// Viewer is who asks to see or decide something about a message.
type Viewer struct {
	ID    string
	Admin bool
	// Agent is true for automation; a human-only action refuses it.
	Agent bool
}

// mayDecide reports whether v may approve or reject m: the person behind the
// recipient session, or an admin.
func (v Viewer) mayDecide(m Message) bool {
	if v.Admin {
		return true
	}
	return m.RecipientActor != "" && strings.EqualFold(m.RecipientActor, strings.TrimSpace(v.ID))
}

// MaySee reports whether v may see m in the project's message list. Delivered
// mail is the project's conversation, open to everyone in it; a held or
// rejected message is still between its sender and the person deciding it,
// and no agent sees one (it would be reading its mail before anyone let it
// through).
func (v Viewer) MaySee(m Message) bool {
	if m.Status == StatusDelivered || m.Status == "" {
		return true
	}
	if v.Agent {
		return false
	}
	return v.Admin || strings.EqualFold(m.SenderActor, strings.TrimSpace(v.ID)) || v.mayDecide(m)
}

// mayRead reports whether v may download m's files.
func (v Viewer) mayRead(m Message) bool {
	// Until a person lets it through, a message has not reached the recipient
	// side, so no agent reads it there; the person deciding may look first.
	if m.Status != StatusDelivered && v.Agent {
		return false
	}
	return v.Admin || strings.EqualFold(m.SenderActor, strings.TrimSpace(v.ID)) || v.mayDecide(m)
}

// ListHeld returns the held messages v may decide, oldest first.
func (h *Hub) ListHeld(ctx context.Context, project string, v Viewer) ([]Message, error) {
	msgs, err := h.ListMessages(ctx, project, "", StatusHeld, 500)
	if err != nil {
		return nil, err
	}
	out := msgs[:0]
	for _, m := range msgs {
		if v.mayDecide(m) {
			out = append(out, m)
		}
	}
	return out, nil
}

// HeldIn returns the held messages of several projects that v may decide,
// oldest first, in one query. Project keys are store roots, as for
// ListHeld. File details are not attached: the building only counts them.
func (h *Hub) HeldIn(ctx context.Context, projects []string, v Viewer) ([]Message, error) {
	projects = normalizeProjectKeys(projects)
	out := []Message{}
	if len(projects) == 0 {
		return out, nil
	}
	if h.database == nil {
		want := make(map[string]bool, len(projects))
		for _, p := range projects {
			want[p] = true
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		for _, m := range h.memMessages {
			if want[m.Project] && m.Status == StatusHeld && v.mayDecide(m) {
				out = append(out, m)
			}
		}
		return out, nil
	}
	args := make([]any, 0, len(projects)+2)
	for _, p := range projects {
		args = append(args, p)
	}
	args = append(args, StatusHeld, maxBuildingRows)
	rows, err := h.database.QueryContext(ctx, `
		SELECT `+messageColumns+`
		  FROM agent_messages
		 WHERE project IN (?`+strings.Repeat(", ?", len(projects)-1)+`) AND status = ?
		 ORDER BY created_at, id LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		if v.mayDecide(m) {
			out = append(out, m)
		}
	}
	return out, rows.Err()
}

// Decide approves (delivers) or rejects a held message. Only the recipient's
// actor or an admin may; to anyone else the message does not exist. Deciding
// a message that is no longer held is ErrNotHeld, so two people clicking at
// once cannot both win.
func (h *Hub) Decide(ctx context.Context, project, id string, approve bool, v Viewer) (Message, error) {
	project = strings.Trim(strings.TrimSpace(project), "/")
	id = strings.TrimSpace(id)
	m, err := h.getMessageByID(ctx, project, id)
	if err != nil {
		return Message{}, err
	}
	if !v.mayDecide(m) {
		return Message{}, ErrNotFound
	}
	status := StatusRejected
	if approve {
		status = StatusDelivered
	}
	decidedBy := strings.TrimSpace(v.ID)
	if decidedBy == "" {
		decidedBy = "local"
	}
	now := h.nowMillis()

	if h.database == nil {
		h.mu.Lock()
		found := false
		for i := range h.memMessages {
			mm := &h.memMessages[i]
			if mm.Project != project || mm.ID != id {
				continue
			}
			found = true
			if mm.Status != StatusHeld {
				h.mu.Unlock()
				return Message{}, fmt.Errorf("%w: it is %s", ErrNotHeld, mm.Status)
			}
			mm.Status, mm.DecidedBy, mm.DecidedAt = status, decidedBy, now
			m = *mm
			m.Files = fileInfos(h.memFiles[id])
		}
		h.mu.Unlock()
		if !found {
			return Message{}, ErrNotFound
		}
	} else {
		res, err := h.database.ExecContext(ctx, `
			UPDATE agent_messages
			   SET status = ?, decided_by = ?, decided_at = ?
			 WHERE project = ? AND id = ? AND status = 'held'
		`, status, decidedBy, now, project, id)
		if err != nil {
			return Message{}, err
		}
		if n, err := res.RowsAffected(); err != nil {
			return Message{}, err
		} else if n == 0 {
			current, err := h.getMessageByID(ctx, project, id)
			if err != nil {
				return Message{}, err
			}
			return Message{}, fmt.Errorf("%w: it is %s", ErrNotHeld, current.Status)
		}
		if m, err = h.getMessageByID(ctx, project, id); err != nil {
			return Message{}, err
		}
	}

	if approve {
		h.wakeInboxWaiters(project)
	}
	h.emit(project, "agent-message", m)
	if approve && m.Notify {
		h.emit(project, "agent-notify", m.ID)
	}
	return m, nil
}

// FileContent returns one attachment of a message, if v may read it. A
// message or file v may not see is ErrNotFound, the same as one that does
// not exist.
func (h *Hub) FileContent(ctx context.Context, project, id, name string, v Viewer) (FileInfo, string, error) {
	project = strings.Trim(strings.TrimSpace(project), "/")
	m, err := h.getMessageByID(ctx, project, strings.TrimSpace(id))
	if err != nil {
		return FileInfo{}, "", err
	}
	if !v.mayRead(m) {
		return FileInfo{}, "", ErrNotFound
	}
	if h.database == nil {
		h.mu.Lock()
		defer h.mu.Unlock()
		for _, f := range h.memFiles[m.ID] {
			if f.Name == name {
				return f.FileInfo, f.content, nil
			}
		}
		return FileInfo{}, "", ErrNotFound
	}
	var f FileInfo
	var content string
	err = h.database.QueryRowContext(ctx, `
		SELECT name, size, sha256, content
		  FROM agent_message_files
		 WHERE message_id = ? AND name = ?
	`, m.ID, name).Scan(&f.Name, &f.Size, &f.SHA256, &content)
	if errors.Is(err, sql.ErrNoRows) {
		return FileInfo{}, "", ErrNotFound
	}
	if err != nil {
		return FileInfo{}, "", err
	}
	return f, content, nil
}

// MayAnswer reports whether v may answer ask a: the person whose session
// asked, or an administrator. A permission prompt is its runner owner's
// alone: answering it runs a tool on their machine, which an administrator
// of the hub was never lent. Without accounts the one local person owns
// every machine, whatever actor a run's sidecar names.
func (v Viewer) MayAnswer(a Ask) bool {
	id := strings.TrimSpace(v.ID)
	if id == "" {
		id = "local"
	}
	if strings.EqualFold(a.ActorID, id) {
		return true
	}
	if a.Kind == AskKindPermission {
		return id == "local"
	}
	return v.Admin
}
