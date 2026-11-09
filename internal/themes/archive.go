package themes

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// maxDirectoryBytes is how much of a zip may be read to learn what is in it,
// 4 KiB for each file a pack may hold. archive/zip reads every entry in the
// central directory before anything can look at one, whatever count the zip
// states: 200 MiB of nothing but entries would be a gigabyte of memory.
const maxDirectoryBytes = 8 << 20

// sniffBytes is enough of a file to tell its type by.
const sniffBytes = 12

var errDirectoryTooLarge = errors.New("zip directory too large")

// directoryBudget caps the reads zip.NewReader makes. The caller lifts it
// afterwards, when reads are of the files, which are counted as they inflate.
type directoryBudget struct {
	archive io.ReaderAt
	left    int64
	lifted  bool
}

func (b *directoryBudget) ReadAt(p []byte, off int64) (int, error) {
	if !b.lifted {
		if int64(len(p)) > b.left {
			return 0, errDirectoryTooLarge
		}
		b.left -= int64(len(p))
	}
	return b.archive.ReadAt(p, off)
}

// parsedArchive is a zip that passed every check that needs no image bytes.
type parsedArchive struct {
	manifest *Manifest
	// stored is the manifest as it goes on disk: what was validated, so a key
	// in another case or given twice never reaches a browser.
	stored []byte
	images []*zip.File
}

// readArchive checks a zip's directory and manifest. It writes nothing, and
// reads no file but theme.json.
func readArchive(archive io.ReaderAt, size int64, limits Limits) (*parsedArchive, error) {
	budget := &directoryBudget{archive: archive, left: maxDirectoryBytes}
	// A name outside the zip comes back as ErrInsecurePath with a usable
	// reader, under a GODEBUG setting; the walk below refuses it either way.
	reader, err := zip.NewReader(budget, size)
	if errors.Is(err, errDirectoryTooLarge) {
		return nil, refusal("the zip has too many files: at most %d", "zip 的檔案太多：最多 %d 個", limits.MaxFiles)
	}
	if err != nil && !errors.Is(err, zip.ErrInsecurePath) {
		return nil, refusal("this file is not a readable zip", "這個檔案不是可讀取的 zip")
	}
	budget.lifted = true

	var manifestEntry *zip.File
	var images []*zip.File
	files := map[string]bool{}
	folded := map[string]bool{}
	for _, entry := range reader.File {
		mode := entry.Mode()
		if strings.HasSuffix(entry.Name, "/") || mode.IsDir() {
			// Ignored, as folders come from the files in them. Still refused
			// if hostile: nothing good comes of such a zip.
			if !mode.IsDir() || mode.Type() != os.ModeDir || !validPath(strings.TrimSuffix(entry.Name, "/")) {
				return nil, refusal("the path %s is not allowed in a pack", "套件不允許路徑 %s", shown(entry.Name))
			}
			continue
		}
		if !mode.IsRegular() {
			return nil, refusal("%s is not a regular file (a link or a device?)", "%s 不是一般檔案（連結或裝置？）", shown(entry.Name))
		}
		if len(files) >= limits.MaxFiles {
			return nil, refusal("the zip has too many files: at most %d", "zip 的檔案太多：最多 %d 個", limits.MaxFiles)
		}
		if entry.Name == ManifestFile {
			manifestEntry = entry
		} else {
			if err := validateImagePath(entry.Name); err != nil {
				return nil, err
			}
			images = append(images, entry)
		}
		// On a disk that ignores case these are one file; refused everywhere,
		// so a pack does not work only where it was made.
		fold := strings.ToLower(entry.Name)
		if folded[fold] {
			return nil, refusal("%s appears twice, or differs from another file only by case", "%s 重複，或與另一個檔案只差大小寫", shown(entry.Name))
		}
		folded[fold] = true
		files[entry.Name] = true
	}
	// A file where another file needs a folder could not be extracted.
	for name := range files {
		for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
			if folded[strings.ToLower(parent)] {
				return nil, refusal("%s is both a file and a folder", "%s 同時是檔案和資料夾", shown(parent))
			}
		}
	}
	if manifestEntry == nil {
		return nil, refusal("the zip has no theme.json at its root", "zip 的根目錄沒有 theme.json")
	}

	raw, err := readManifestEntry(manifestEntry, limits.MaxManifestBytes)
	if err != nil {
		return nil, err
	}
	manifest, err := parseManifest(raw)
	if err != nil {
		return nil, err
	}
	named := manifest.images()
	for name := range named {
		if !files[name] {
			return nil, refusal("theme.json names %s, which is not in the zip", "theme.json 指定的 %s 不在 zip 中", shown(name))
		}
	}
	for _, entry := range images {
		if !named[entry.Name] {
			return nil, refusal("%s is in the zip but theme.json does not name it", "%s 在 zip 中，但 theme.json 沒有指定它", shown(entry.Name))
		}
	}
	stored, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	return &parsedArchive{manifest: manifest, stored: stored, images: images}, nil
}

func readManifestEntry(entry *zip.File, limit int64) ([]byte, error) {
	source, err := entry.Open()
	if err != nil {
		return nil, damaged(entry.Name)
	}
	defer source.Close()
	raw, err := io.ReadAll(io.LimitReader(source, limit+1))
	if err != nil {
		return nil, damaged(entry.Name)
	}
	if int64(len(raw)) > limit {
		return nil, refusal("theme.json is over %d bytes", "theme.json 超過 %d 位元組", limit)
	}
	return raw, nil
}

func damaged(name string) error {
	return refusal("%s in the zip is damaged, or is not the size the zip says", "zip 中的 %s 已損毀，或大小與 zip 所記載的不符", shown(name))
}

// extract writes the pack into dir under root and returns its size in bytes.
// Only bytes really inflated count: what the zip's headers claim is ignored.
func extract(ctx context.Context, root *os.Root, dir string, parsed *parsedArchive, limits Limits) (int64, error) {
	if err := writeNew(root, filepath.Join(dir, ManifestFile), parsed.stored); err != nil {
		return 0, err
	}
	total := int64(len(parsed.stored))
	for _, entry := range parsed.images {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		written, err := extractImage(root, dir, entry, limits, total)
		if err != nil {
			return 0, err
		}
		total += written
	}
	return total, nil
}

func writeNew(root *os.Root, name string, data []byte) error {
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

func extractImage(root *os.Root, dir string, entry *zip.File, limits Limits, total int64) (int64, error) {
	source, err := entry.Open()
	if err != nil {
		return 0, damaged(entry.Name)
	}
	defer source.Close()
	head := make([]byte, sniffBytes)
	n, err := io.ReadFull(source, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return 0, damaged(entry.Name)
	}
	head = head[:n]
	if !matchesImageType(entry.Name, head) {
		return 0, refusal("%s is not the image type its extension says", "%s 不是副檔名所標示的圖片類型", shown(entry.Name))
	}

	target := filepath.Join(dir, filepath.FromSlash(entry.Name))
	if err := root.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return 0, err
	}
	out, err := root.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return 0, err
	}
	defer out.Close()
	// One byte past whichever limit comes first says it was passed, and is as
	// far as a bomb gets.
	limit := min(limits.MaxFileBytes, limits.MaxTotalBytes-total)
	counted := &readErrorKeeper{reader: source}
	written, err := io.Copy(out, io.LimitReader(io.MultiReader(strings.NewReader(string(head)), counted), limit+1))
	if counted.err != nil {
		return 0, damaged(entry.Name)
	}
	if err != nil {
		return 0, err
	}
	if written > limits.MaxFileBytes {
		return 0, refusal("%s is over %d bytes", "%s 超過 %d 位元組", shown(entry.Name), limits.MaxFileBytes)
	}
	if written > limit {
		return 0, refusal("the pack is over %d bytes uncompressed", "套件解壓縮後超過 %d 位元組", limits.MaxTotalBytes)
	}
	return written, nil
}

// readErrorKeeper tells a zip that will not inflate, which is the uploader's
// to fix, from a disk that will not write, which is not.
type readErrorKeeper struct {
	reader io.Reader
	err    error
}

func (r *readErrorKeeper) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		r.err = err
	}
	return n, err
}
