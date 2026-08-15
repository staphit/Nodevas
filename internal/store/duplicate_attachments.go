package store

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type duplicateAttachment struct {
	name string
	data []byte
}

const (
	maxDuplicateAttachmentFiles      = 1_000
	maxDuplicateAttachmentFileBytes  = int64(MaxAttachmentBytes)
	maxDuplicateAttachmentTotalBytes = int64(256 << 20)
)

type duplicateAttachmentLimits struct {
	maxFiles      int
	maxFileBytes  int64
	maxTotalBytes int64
}

var defaultDuplicateAttachmentLimits = duplicateAttachmentLimits{
	maxFiles:      maxDuplicateAttachmentFiles,
	maxFileBytes:  maxDuplicateAttachmentFileBytes,
	maxTotalBytes: maxDuplicateAttachmentTotalBytes,
}

// readDuplicateAttachments takes a stable, root-confined snapshot of the
// source node's flat attachment directory. Attachments are created by
// SaveAttachment as regular files; silently following a link or skipping an
// unexpected child would turn a duplicate into either an escape or an
// incomplete copy, so both are errors here.
func readDuplicateAttachments(dir string) ([]duplicateAttachment, error) {
	return readDuplicateAttachmentsWithLimits(dir, defaultDuplicateAttachmentLimits)
}

func readDuplicateAttachmentsWithLimits(dir string, limits duplicateAttachmentLimits) ([]duplicateAttachment, error) {
	if limits.maxFiles < 0 || limits.maxFileBytes < 0 || limits.maxTotalBytes < 0 {
		return nil, fmt.Errorf("invalid duplicate attachment limits")
	}
	dirInfo, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if dirInfo.Mode()&os.ModeSymlink != 0 || !dirInfo.IsDir() {
		return nil, fmt.Errorf("attachment source is not a regular directory")
	}

	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	openedInfo, err := root.Stat(".")
	if err != nil {
		return nil, err
	}
	if !os.SameFile(dirInfo, openedInfo) {
		return nil, fmt.Errorf("attachment source changed while duplicating")
	}
	directory, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer directory.Close()

	attachments := make([]duplicateAttachment, 0)
	entryCount := 0
	var totalBytes int64
	for {
		// Read directory entries in bounded batches too; the file-count limit
		// must not require first materializing an arbitrarily large directory.
		entries, readErr := directory.ReadDir(128)
		for _, entry := range entries {
			name := entry.Name()
			// Count every child, including stale atomic-write temp files. Temp
			// files are not copied, but they must not bypass the traversal cap.
			if entryCount >= limits.maxFiles {
				return nil, fmt.Errorf("attachment file-count limit exceeded (max %d)", limits.maxFiles)
			}
			entryCount++
			if strings.HasPrefix(name, TmpPrefix) {
				continue
			}
			if !filepath.IsLocal(name) || filepath.Base(name) != name {
				return nil, fmt.Errorf("unsafe attachment name %q", name)
			}
			info, err := root.Lstat(name)
			if err != nil {
				return nil, err
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return nil, fmt.Errorf("attachment %q is a symbolic link", name)
			}
			if !info.Mode().IsRegular() {
				return nil, fmt.Errorf("attachment %q is not a regular file", name)
			}
			if info.Size() < 0 || info.Size() > limits.maxFileBytes {
				return nil, fmt.Errorf("attachment %q exceeds single-file limit of %d bytes", name, limits.maxFileBytes)
			}

			remainingBytes := limits.maxTotalBytes - totalBytes
			if remainingBytes < 0 || info.Size() > remainingBytes {
				return nil, fmt.Errorf("attachment aggregate limit exceeded (max %d bytes)", limits.maxTotalBytes)
			}
			data, err := readDuplicateAttachment(root, name, info, limits.maxFileBytes, remainingBytes)
			if err != nil {
				return nil, err
			}
			totalBytes += int64(len(data))
			attachments = append(attachments, duplicateAttachment{name: name, data: data})
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
	}
	return attachments, nil
}

func readDuplicateAttachment(root *os.Root, name string, before fs.FileInfo, maxFileBytes, remainingBytes int64) ([]byte, error) {
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	opened, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(before, opened) || before.Size() != opened.Size() {
		return nil, fmt.Errorf("attachment %q changed while duplicating", name)
	}
	size := opened.Size()
	if size < 0 || size > maxFileBytes {
		return nil, fmt.Errorf("attachment %q exceeds single-file limit of %d bytes", name, maxFileBytes)
	}
	if size > remainingBytes {
		return nil, fmt.Errorf("attachment aggregate limit exceeded (max snapshot exhausted before %q)", name)
	}

	// The extra byte turns growth during the read into a deterministic error
	// without ever allocating beyond the configured single-file bound.
	data, err := io.ReadAll(io.LimitReader(file, size+1))
	if err != nil {
		return nil, err
	}
	after, err := file.Stat()
	if err != nil {
		return nil, err
	}
	pathAfter, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != size || after.Size() != size || pathAfter.Size() != size ||
		!os.SameFile(opened, after) || !os.SameFile(opened, pathAfter) {
		return nil, fmt.Errorf("attachment %q changed while duplicating", name)
	}
	return data, nil
}

func requireSafeDuplicateAttachmentDir(dir string) error {
	dirInfo, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if dirInfo.Mode()&os.ModeSymlink != 0 || !dirInfo.IsDir() {
		return fmt.Errorf("duplicate attachment destination is not a regular directory")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	openedInfo, err := root.Stat(".")
	if err != nil {
		return err
	}
	if !os.SameFile(dirInfo, openedInfo) {
		return fmt.Errorf("duplicate attachment destination changed while duplicating")
	}
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	defer directory.Close()
	entries, err := directory.ReadDir(1)
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if len(entries) != 0 {
		return fmt.Errorf("duplicate attachment destination is not empty")
	}
	return nil
}
