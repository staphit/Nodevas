// Writing a node's file attachments. Reading them back is plain file serving,
// so it stays in the HTTP layer; creating one is a store write like any other.

package store

import (
	"errors"
	"fmt"
	"io"
	"os"

	"nodevas/internal/engine"
)

// SaveAttachment streams an upload into nodes/<id>.files/, under the name the
// uploader asked for once it has been reduced to a safe basename, and returns
// the name it actually landed on.
//
// The name is picked and claimed while holding the media lock, because
// UniqueAttachmentPath only looks: two uploads of the same filename racing
// each other would otherwise agree on the same free name. A failed copy takes
// the partial file with it, so a caller never has to clean up after this.
func (s *Store) SaveAttachment(nodeID, filename string, body io.Reader) (string, error) {
	if !engine.ValidNodeID(nodeID) {
		return "", errors.New("invalid node id")
	}
	s.mediaMu.Lock()
	defer s.mediaMu.Unlock()
	dir := s.NodeFilesDir(nodeID)
	if err := MkdirAllProjectPath(s.root, dir, 0o755); err != nil {
		return "", err
	}
	target, name := UniqueAttachmentPath(dir, SanitizeAttachmentName(filename))
	s.markSelfWrite(target)
	file, err := createProjectFileExclusive(s.root, target, 0o644)
	if err != nil {
		return "", err
	}
	discard := func(cause error) (string, error) {
		if err := s.removePath(target); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", errors.Join(cause, fmt.Errorf("remove %s: %w", target, err))
		}
		return "", cause
	}
	if _, err := io.Copy(file, body); err != nil {
		file.Close()
		return discard(err)
	}
	if err := file.Close(); err != nil {
		return discard(err)
	}
	return name, nil
}
