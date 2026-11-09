package themes

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"nodevas/internal/locale"
)

// ManifestFile is the manifest's name at the root of a zip and of a stored
// pack.
const ManifestFile = "theme.json"

const (
	manifestSchema = 1
	maxCanvas      = 8192
	maxRooms       = 256
	maxCharacters  = 64
	maxScenes      = 256
	maxMatches     = 16
	maxTextRunes   = 64
	maxPathBytes   = 200
	// maxShownRunes is how much of a name from a zip an error repeats.
	maxShownRunes = 80
)

var (
	idPattern   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
	namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
)

// reservedIDs mean something else where a pack id goes: "none" is the web
// preference's value for the built-in people, "default" the route
// PUT /api/themes/default.
var reservedIDs = map[string]bool{"none": true, "default": true}

// imageTypes are the only files a pack holds, by extension: a file is served
// as the type its extension says, never as what its bytes look like.
var imageTypes = map[string]string{
	".webp": "image/webp",
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
}

// ContentType is the type a pack's image is served as, and false for a name
// that is no image a pack may hold.
func ContentType(name string) (string, bool) {
	contentType, ok := imageTypes[path.Ext(name)]
	return contentType, ok
}

// matchesImageType reports whether a file's leading bytes are the type its
// extension says, so that a page cannot arrive dressed as a picture.
func matchesImageType(name string, head []byte) bool {
	switch path.Ext(name) {
	case ".webp":
		return len(head) >= 12 && string(head[:4]) == "RIFF" && string(head[8:12]) == "WEBP"
	case ".png":
		return bytes.HasPrefix(head, []byte("\x89PNG\r\n\x1a\n"))
	case ".jpg", ".jpeg":
		return bytes.HasPrefix(head, []byte{0xFF, 0xD8, 0xFF})
	}
	return false
}

// Name is a pack's name in the interface's languages.
type Name struct {
	En   string `json:"en"`
	ZhTW string `json:"zh-TW,omitempty"`
}

// Manifest is a pack's theme.json.
type Manifest struct {
	Schema     int                  `json:"schema"`
	ID         string               `json:"id"`
	Name       Name                 `json:"name"`
	Version    string               `json:"version"`
	Author     string               `json:"author"`
	Canvas     []int                `json:"canvas"`
	Rooms      map[string]string    `json:"rooms"`
	Characters map[string]Character `json:"characters"`
}

// Character is one figure and the scenes it can be drawn in.
type Character struct {
	Match  Match            `json:"match"`
	Scenes map[string]Scene `json:"scenes"`
}

// Match says which agents and models a character stands for.
type Match struct {
	Agents []string `json:"agents"`
	Models []string `json:"models"`
}

// Scene places one image of a character in a room.
type Scene struct {
	Room  string    `json:"room"`
	Image string    `json:"image"`
	X     int       `json:"x"`
	Y     int       `json:"y"`
	W     int       `json:"w"`
	H     int       `json:"h"`
	Box   []float64 `json:"box"`
}

// refusal is a zip the uploader has to fix.
func refusal(english, chinese string, args ...any) error {
	return &Error{status: http.StatusBadRequest, err: locale.Errorf(english, chinese, args...)}
}

// shown is a name from a zip as an error repeats it: quoted, so a control
// character cannot pass for layout, and short.
func shown(name string) string {
	if utf8.RuneCountInString(name) > maxShownRunes {
		name = string([]rune(name)[:maxShownRunes]) + "..."
	}
	return strconv.Quote(name)
}

// parseManifest decodes and validates a theme.json strictly: an unknown field
// anywhere, or anything after the one value, refuses it.
func parseManifest(data []byte) (*Manifest, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return nil, refusal("theme.json is not valid: %v", "theme.json 無效：%v", err)
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, refusal("theme.json must hold one JSON value", "theme.json 只能有一個 JSON 值")
	}
	if err := manifest.validate(); err != nil {
		return nil, err
	}
	manifest.fillEmpties()
	return &manifest, nil
}

func (m *Manifest) validate() error {
	if m.Schema != manifestSchema {
		return refusal("theme.json schema must be %d", "theme.json 的 schema 必須是 %d", manifestSchema)
	}
	if !ValidID(m.ID) {
		return refusal("the pack id %s is not valid: lowercase letters, digits and hyphens, at most 40",
			"套件代號 %s 無效：只能用小寫字母、數字與連字號，最多 40 個字元", shown(m.ID))
	}
	if reservedIDs[m.ID] {
		return refusal("the pack id %s is reserved; choose another", "套件代號 %s 已保留，請換一個", shown(m.ID))
	}
	if m.Version == "" || !shortText(m.Version) {
		return refusal("version must be 1 to %d characters", "version 必須是 1 到 %d 個字元", maxTextRunes)
	}
	if !shortText(m.Author) {
		return refusal("author must be at most %d characters", "author 最多 %d 個字元", maxTextRunes)
	}
	if m.Name.En == "" {
		return refusal("name needs an English name (en)", "name 必須有英文名稱（en）")
	}
	if !shortText(m.Name.En) || !shortText(m.Name.ZhTW) {
		return refusal("each name must be at most %d characters", "每個名稱最多 %d 個字元", maxTextRunes)
	}
	if len(m.Canvas) != 2 || m.Canvas[0] < 1 || m.Canvas[0] > maxCanvas || m.Canvas[1] < 1 || m.Canvas[1] > maxCanvas {
		return refusal("canvas must be two integers from 1 to %d", "canvas 必須是兩個 1 到 %d 的整數", maxCanvas)
	}
	if len(m.Rooms) > maxRooms {
		return refusal("a pack can have at most %d rooms", "一個套件最多 %d 個房間", maxRooms)
	}
	for id, image := range m.Rooms {
		if !namePattern.MatchString(id) {
			return refusal("the room id %s is not valid", "房間代號 %s 無效", shown(id))
		}
		if err := validateImagePath(image); err != nil {
			return err
		}
	}
	if len(m.Characters) > maxCharacters {
		return refusal("a pack can have at most %d characters", "一個套件最多 %d 個角色", maxCharacters)
	}
	for id, character := range m.Characters {
		if !namePattern.MatchString(id) {
			return refusal("the character id %s is not valid", "角色代號 %s 無效", shown(id))
		}
		if err := m.validateCharacter(id, character); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manifest) validateCharacter(id string, character Character) error {
	for _, list := range [][]string{character.Match.Agents, character.Match.Models} {
		if len(list) > maxMatches {
			return refusal("character %s: a match list can have at most %d entries", "角色 %s：比對清單最多 %d 項", shown(id), maxMatches)
		}
		for _, entry := range list {
			if !shortText(entry) {
				return refusal("character %s: a match entry must be at most %d characters", "角色 %s：比對項目最多 %d 個字元", shown(id), maxTextRunes)
			}
		}
	}
	if len(character.Scenes) > maxScenes {
		return refusal("character %s can have at most %d scenes", "角色 %s 最多 %d 個場景", shown(id), maxScenes)
	}
	for name, scene := range character.Scenes {
		if !namePattern.MatchString(name) {
			return refusal("the scene name %s is not valid", "場景名稱 %s 無效", shown(name))
		}
		if err := m.validateScene(id, name, scene); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manifest) validateScene(character, name string, scene Scene) error {
	if _, ok := m.Rooms[scene.Room]; !ok {
		return refusal("scene %s/%s names the unknown room %s", "場景 %s/%s 指定了不存在的房間 %s", character, name, shown(scene.Room))
	}
	if err := validateImagePath(scene.Image); err != nil {
		return err
	}
	width, height := m.Canvas[0], m.Canvas[1]
	// Compared by what is left of the canvas, as x + w could overflow.
	if scene.W < 1 || scene.H < 1 || scene.X < 0 || scene.Y < 0 ||
		scene.W > width || scene.H > height || scene.X > width-scene.W || scene.Y > height-scene.H {
		return refusal("scene %s/%s: its rectangle must lie inside the canvas", "場景 %s/%s：矩形必須在畫布內", character, name)
	}
	box := scene.Box
	if len(box) != 4 || box[0] < 0 || box[1] < 0 || box[0] >= box[2] || box[1] >= box[3] ||
		box[2] > float64(width) || box[3] > float64(height) {
		return refusal("scene %s/%s: box must be left, top, right, bottom inside the canvas", "場景 %s/%s：box 必須是畫布內的左、上、右、下", character, name)
	}
	return nil
}

// images is every file the manifest names.
func (m *Manifest) images() map[string]bool {
	named := make(map[string]bool, len(m.Rooms))
	for _, image := range m.Rooms {
		named[image] = true
	}
	for _, character := range m.Characters {
		for _, scene := range character.Scenes {
			named[scene.Image] = true
		}
	}
	return named
}

// fillEmpties makes what a pack left out an empty list or map in what is
// stored, so the reader of the manifest has one shape to handle.
func (m *Manifest) fillEmpties() {
	if m.Rooms == nil {
		m.Rooms = map[string]string{}
	}
	if m.Characters == nil {
		m.Characters = map[string]Character{}
	}
	for id, character := range m.Characters {
		if character.Match.Agents == nil {
			character.Match.Agents = []string{}
		}
		if character.Match.Models == nil {
			character.Match.Models = []string{}
		}
		if character.Scenes == nil {
			character.Scenes = map[string]Scene{}
		}
		m.Characters[id] = character
	}
}

// ValidID reports whether id has the form of a pack id. A reserved id has it
// too; it just cannot be imported.
func ValidID(id string) bool { return idPattern.MatchString(id) }

func shortText(text string) bool { return utf8.RuneCountInString(text) <= maxTextRunes }

// validPath is the only form a path in a pack may have. The characters and
// segment endings past the contract's list are those Windows reads as
// something else (a stream, a wildcard, the same name without its dot), so a
// name means one file wherever the server runs.
func validPath(name string) bool {
	if name == "" || len(name) > maxPathBytes || !utf8.ValidString(name) || strings.ContainsAny(name, "\\:*?\"<>|") {
		return false
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	for _, segment := range strings.Split(name, "/") {
		if segment == "" || segment == "." || segment == ".." ||
			strings.HasSuffix(segment, ".") || strings.HasSuffix(segment, " ") {
			return false
		}
	}
	return filepath.IsLocal(filepath.FromSlash(name))
}

func validateImagePath(name string) error {
	if !validPath(name) {
		return refusal("the path %s is not allowed in a pack", "套件不允許路徑 %s", shown(name))
	}
	if _, ok := ContentType(name); !ok {
		return refusal("%s is not allowed: a pack holds only .webp, .png, .jpg and .jpeg images",
			"不允許 %s：套件只能有 .webp、.png、.jpg、.jpeg 圖片", shown(name))
	}
	return nil
}
