package packs

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// The limits docs/view-packs.md promises. They are checked against what the
// zip actually holds, never against what its headers claim.
const (
	// Format is the one pack.json format this server reads.
	Format = 1
	// MaxFiles is how many files a zip may hold. Directory entries are not
	// files: a zipped folder has one per folder.
	MaxFiles = 200
	// MaxUnpacked bounds the decompressed bytes of every file together,
	// including the files a pack does not use.
	MaxUnpacked = 20 << 20
	// MaxImage bounds one sprite image.
	MaxImage = 2 << 20
	// MaxZip bounds the zip itself. A zip of stored (uncompressed) files is a
	// little larger than what it unpacks to, so it gets a megabyte of headers.
	MaxZip = MaxUnpacked + 1<<20
	// maxManifest bounds pack.json. It holds a name and a few dozen short
	// strings; anything near this size is not a pack.
	maxManifest = 64 << 10
	// maxNameLength bounds one display name, in characters.
	maxNameLength = 80
	// maxSpritePath bounds one sprite path.
	maxSpritePath = 255
)

// The two layouts a pack can restyle.
const (
	LayoutRooms = "rooms"
	LayoutPath  = "path"
)

// SpriteKeys lists the sprite keys each layout draws (docs/view-packs.md).
// A key outside its layout's list is refused rather than ignored: it is
// almost always a typo, and a silently ignored typo looks like a broken pack.
var SpriteKeys = map[string][]string{
	LayoutRooms: {
		"agent.working", "agent.reviewing", "agent.asking-user", "agent.awaiting-accept",
		"agent.waiting-ci", "agent.failed", "agent.idle", "agent.offline", "agent",
		"agent.standing", "desk.empty", "table",
	},
	LayoutPath: {
		"monster.attackable", "monster.locked", "monster.defeated", "monster",
		"boss", "boss.defeated", "hero", "tile.corridor", "tile.arena",
		"gate.closed", "gate.open", "chest.closed", "chest.open", "column", "barrel",
	},
}

var (
	idPattern     = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)
	colourPattern = regexp.MustCompile(`^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)
	// paletteKeyPattern keeps a palette name to a word: the web client may
	// turn it into a CSS custom property, and a pack carries no CSS.
	paletteKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)
	spritePathPattern = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)
	// urlPattern finds anything a browser or a reader could follow: a scheme
	// with "//", a protocol-relative "//host", the schemes that need no
	// slashes, and a bare "www." host.
	urlPattern = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://|^//|\b(data|javascript|vbscript|blob|file|mailto):|\bwww\.)`)
)

// reservedPaths are the names the installed folder uses for itself. A sprite
// cannot take one, compared without case because macOS and Windows folders
// do not tell "Pack.JSON" from "pack.json".
var reservedPaths = map[string]bool{manifestName: true, installName: true}

// InvalidError is a pack that breaks one of the rules. Its message names the
// rule, so the person installing it knows what to fix.
type InvalidError struct {
	Reason string
}

func (e *InvalidError) Error() string { return "invalid pack: " + e.Reason }

func invalid(format string, args ...any) error {
	return &InvalidError{Reason: fmt.Sprintf(format, args...)}
}

// Manifest is pack.json.
type Manifest struct {
	Format  int               `json:"format"`
	ID      string            `json:"id"`
	Name    map[string]string `json:"name"`
	Layout  string            `json:"layout"`
	Palette map[string]string `json:"palette,omitempty"`
	Sprites map[string]string `json:"sprites,omitempty"`
}

// Kind is what an image's bytes show it to be.
type Kind string

const (
	KindPNG  Kind = "png"
	KindWebP Kind = "webp"
	KindSVG  Kind = "svg"
)

// ContentType is the only media type a pack file is ever served as.
func (k Kind) ContentType() string {
	switch k {
	case KindPNG:
		return "image/png"
	case KindWebP:
		return "image/webp"
	case KindSVG:
		return "image/svg+xml"
	}
	return ""
}

// DetectKind reads an image's kind from its contents, ignoring its name, and
// returns "" for anything that is not a PNG, a WebP or an SVG.
func DetectKind(data []byte) Kind {
	switch {
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return KindPNG
	case len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return KindWebP
	}
	// An SVG is XML text. Requiring text before looking for the tag keeps a
	// binary file that happens to contain "<svg" from being served as one.
	text := bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if !utf8.Valid(text) || bytes.IndexByte(text, 0) >= 0 {
		return ""
	}
	trimmed := bytes.TrimLeft(text, " \t\r\n")
	if len(trimmed) == 0 || trimmed[0] != '<' || !bytes.Contains(trimmed, []byte("<svg")) {
		return ""
	}
	return KindSVG
}

// parsed is a zip that passed every rule: its pack.json as written, and the
// images its sprites name, keyed by sprite path.
type parsed struct {
	manifest Manifest
	raw      []byte
	images   map[string][]byte
}

// parseZip checks a zip against every rule in docs/view-packs.md and returns
// what an install keeps. It reads in memory only: nothing touches the disk
// until the whole pack has passed.
func parseZip(data []byte) (*parsed, error) {
	if len(data) == 0 {
		return nil, invalid("the zip is empty")
	}
	if len(data) > MaxZip {
		return nil, invalid("the zip is larger than %d MB", MaxZip>>20)
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, invalid("not a readable zip file: %v", err)
	}

	files := make(map[string]*zip.File, len(reader.File))
	count := 0
	for _, f := range reader.File {
		if err := checkEntry(f); err != nil {
			return nil, err
		}
		if f.FileInfo().IsDir() {
			continue
		}
		if _, dup := files[f.Name]; dup {
			return nil, invalid("%q appears twice in the zip", f.Name)
		}
		files[f.Name] = f
		count++
		if count > MaxFiles {
			return nil, invalid("the zip has more than %d files", MaxFiles)
		}
	}

	prefix, err := findManifest(reader.File, files)
	if err != nil {
		return nil, err
	}
	budget := &unpackBudget{left: MaxUnpacked}
	manifestFile := files[prefix+manifestName]
	raw, err := budget.read(manifestFile, maxManifest)
	if err != nil {
		var tooBig *entryTooLarge
		if errors.As(err, &tooBig) {
			return nil, invalid("pack.json is larger than %d KB", maxManifest>>10)
		}
		return nil, err
	}
	manifest, err := parseManifest(raw)
	if err != nil {
		return nil, err
	}

	// Which zip entry each sprite path is, so the pass below knows what to keep.
	wanted := map[string]string{}
	for _, key := range sortedKeys(manifest.Sprites) {
		path := manifest.Sprites[key]
		name := prefix + path
		if files[name] == nil {
			return nil, invalid("sprite %q: %s is not in the zip", key, path)
		}
		wanted[name] = path
	}

	// Every file is decompressed, used or not: the unpacked limit is about
	// what the zip holds, and reading it is the only honest way to know.
	images := map[string][]byte{}
	for _, f := range reader.File {
		if f.FileInfo().IsDir() || f == manifestFile {
			continue
		}
		path, keep := wanted[f.Name]
		if !keep {
			if err := budget.discard(f); err != nil {
				return nil, err
			}
			continue
		}
		content, err := budget.read(f, MaxImage)
		if err != nil {
			var tooBig *entryTooLarge
			if errors.As(err, &tooBig) {
				return nil, invalid("%s is larger than %d MB", path, MaxImage>>20)
			}
			return nil, err
		}
		if DetectKind(content) == "" {
			return nil, invalid("%s is not a PNG, WebP or SVG image", path)
		}
		images[path] = content
	}
	return &parsed{manifest: *manifest, raw: raw, images: images}, nil
}

// checkEntry refuses an entry whose name could land outside the pack folder,
// or that is anything but a plain file or folder.
func checkEntry(f *zip.File) error {
	name := f.Name
	switch {
	case name == "":
		return invalid("the zip has an entry with no name")
	case strings.Contains(name, `\`):
		return invalid("%q uses a backslash; paths must use /", name)
	case strings.HasPrefix(name, "/") || (len(name) >= 2 && name[1] == ':'):
		return invalid("%q is an absolute path", name)
	case strings.ContainsRune(name, 0):
		return invalid("%q has a NUL character in its name", name)
	}
	for _, segment := range strings.Split(strings.TrimSuffix(name, "/"), "/") {
		switch segment {
		case "..":
			return invalid("%q uses ..; paths must stay inside the zip", name)
		case "", ".":
			return invalid("%q is not a plain relative path", name)
		}
	}
	mode := f.Mode()
	if mode&fs.ModeSymlink != 0 {
		return invalid("%q is a link; a pack holds only files", name)
	}
	if !mode.IsRegular() && !mode.IsDir() {
		return invalid("%q is not a plain file", name)
	}
	return nil
}

// macOSMetadata is the folder Finder adds to every zip it makes, beside the
// folder that was zipped. It is ignored when looking for the pack's folder.
const macOSMetadata = "__MACOSX/"

// findManifest returns the folder prefix pack.json sits under: "" at the zip
// root, or "<folder>/" when the zip holds one top-level folder.
func findManifest(entries []*zip.File, files map[string]*zip.File) (string, error) {
	if files[manifestName] != nil {
		return "", nil
	}
	tops := map[string]bool{}
	for _, f := range entries {
		if strings.HasPrefix(f.Name, macOSMetadata) {
			continue
		}
		top, _, nested := strings.Cut(f.Name, "/")
		if !nested {
			// A loose file at the root, and no pack.json beside it.
			return "", invalid("pack.json must sit at the zip root or inside one top-level folder")
		}
		tops[top] = true
	}
	if len(tops) != 1 {
		return "", invalid("pack.json must sit at the zip root or inside one top-level folder")
	}
	for top := range tops {
		if files[top+"/"+manifestName] != nil {
			return top + "/", nil
		}
		return "", invalid("there is no pack.json in %s/", top)
	}
	return "", nil
}

// parseManifest reads pack.json and checks every rule it must follow. List
// runs it again on what is on disk, so a pack edited by hand is held to the
// same rules as one that was installed.
func parseManifest(raw []byte) (*Manifest, error) {
	var tree any
	if err := json.Unmarshal(raw, &tree); err != nil {
		return nil, invalid("pack.json does not parse: %v", err)
	}
	if _, ok := tree.(map[string]any); !ok {
		return nil, invalid("pack.json must be a JSON object")
	}
	// Every string, keys included and wherever it sits, so a URL cannot hide
	// in a field this server does not otherwise look at.
	if found := findURL(tree); found != "" {
		return nil, invalid("pack.json contains a URL (%q); a pack cannot load anything from another site", found)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var m Manifest
	if err := decoder.Decode(&m); err != nil {
		return nil, invalid("pack.json does not match format %d: %v", Format, err)
	}
	if decoder.More() {
		return nil, invalid("pack.json has content after its object")
	}
	if err := m.validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

func (m *Manifest) validate() error {
	if m.Format != Format {
		return invalid("format must be %d, not %d", Format, m.Format)
	}
	if !idPattern.MatchString(m.ID) {
		return invalid("id %q must be 1 to 40 lowercase letters, digits or hyphens", m.ID)
	}
	if strings.TrimSpace(m.Name["en"]) == "" {
		return invalid(`name must have an "en" entry`)
	}
	for locale, name := range m.Name {
		if locale == "" || len(locale) > 35 {
			return invalid("name has an invalid language tag %q", locale)
		}
		if utf8.RuneCountInString(name) > maxNameLength {
			return invalid("name %q is longer than %d characters", locale, maxNameLength)
		}
	}
	keys, ok := SpriteKeys[m.Layout]
	if !ok {
		return invalid("layout must be %q or %q, not %q", LayoutRooms, LayoutPath, m.Layout)
	}
	for _, key := range sortedKeys(m.Palette) {
		if !paletteKeyPattern.MatchString(key) {
			return invalid("palette name %q must be lowercase letters, digits or hyphens", key)
		}
		if !colourPattern.MatchString(m.Palette[key]) {
			return invalid("palette %q is %q; colours must be #rgb or #rrggbb", key, m.Palette[key])
		}
	}
	known := map[string]bool{}
	for _, key := range keys {
		known[key] = true
	}
	for _, key := range sortedKeys(m.Sprites) {
		if !known[key] {
			return invalid("sprite %q is not a %s sprite", key, m.Layout)
		}
		if err := checkSpritePath(m.Sprites[key]); err != nil {
			return invalid("sprite %q: %v", key, err)
		}
	}
	return nil
}

// checkSpritePath holds a sprite's path to the characters the web client
// accepts in a pack file URL, so the URL needs no encoding and cannot be
// read two ways.
func checkSpritePath(path string) error {
	switch {
	case path == "":
		return errors.New("the path is empty")
	case len(path) > maxSpritePath:
		return fmt.Errorf("the path is longer than %d characters", maxSpritePath)
	case !spritePathPattern.MatchString(path):
		return fmt.Errorf("%q may use only letters, digits, '.', '_', '-' and '/'", path)
	case strings.HasPrefix(path, "/"):
		return fmt.Errorf("%q is an absolute path", path)
	}
	// The first segment, so neither "install.json" nor "install.json/a.png"
	// can take the name.
	first, _, _ := strings.Cut(path, "/")
	if reservedPaths[strings.ToLower(first)] {
		return fmt.Errorf("%q is a name the pack folder uses for itself", path)
	}
	for _, segment := range strings.Split(path, "/") {
		switch segment {
		case "..":
			return fmt.Errorf("%q uses ..; paths must stay inside the pack", path)
		case "", ".":
			return fmt.Errorf("%q is not a plain relative path", path)
		}
	}
	return nil
}

// findURL returns the first string in a decoded JSON value that looks like a
// URL, or "".
func findURL(value any) string {
	switch v := value.(type) {
	case string:
		if urlPattern.MatchString(v) {
			return v
		}
	case []any:
		for _, item := range v {
			if found := findURL(item); found != "" {
				return found
			}
		}
	case map[string]any:
		for _, key := range sortedKeys(v) {
			if urlPattern.MatchString(key) {
				return key
			}
			if found := findURL(v[key]); found != "" {
				return found
			}
		}
	}
	return ""
}

// unpackBudget is what is left of MaxUnpacked. Each read stops one byte past
// what it may take, so a zip bomb costs at most the limit to find out.
type unpackBudget struct {
	left int64
}

// entryTooLarge is one entry over its own cap, as opposed to the whole zip
// over MaxUnpacked.
type entryTooLarge struct{}

func (*entryTooLarge) Error() string { return "entry too large" }

func (b *unpackBudget) read(f *zip.File, limit int64) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, invalid("cannot read %s: %v", f.Name, err)
	}
	defer rc.Close()
	allowed := min(limit, b.left)
	content, err := io.ReadAll(io.LimitReader(rc, allowed+1))
	if err != nil {
		return nil, invalid("cannot read %s: %v", f.Name, err)
	}
	if int64(len(content)) > allowed {
		if allowed == limit {
			return nil, &entryTooLarge{}
		}
		return nil, invalid("the zip unpacks to more than %d MB", MaxUnpacked>>20)
	}
	b.left -= int64(len(content))
	return content, nil
}

func (b *unpackBudget) discard(f *zip.File) error {
	rc, err := f.Open()
	if err != nil {
		return invalid("cannot read %s: %v", f.Name, err)
	}
	defer rc.Close()
	n, err := io.Copy(io.Discard, io.LimitReader(rc, b.left+1))
	if err != nil {
		return invalid("cannot read %s: %v", f.Name, err)
	}
	if n > b.left {
		return invalid("the zip unpacks to more than %d MB", MaxUnpacked>>20)
	}
	b.left -= n
	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
