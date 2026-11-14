package themes

import (
	"net/http"
	"reflect"
	"testing"
)

// A scene can play a clip in place of its still picture: an .mp4 or a .webm,
// named only by the scene's video. The image stays, as the still.

// The two clip types by their leading bytes, with a little body.
var (
	mp4Clip  = []byte("\x00\x00\x00\x18ftypisom\x00\x00\x02\x00body")
	webmClip = []byte("\x1a\x45\xdf\xa3\x9f\x42\x86\x81\x01body")
)

// filmedPack is a good pack whose scene plays the clip at path, and its files.
func filmedPack(path string, clip []byte) (map[string]any, map[string][]byte) {
	doc, files := manifestDoc(), goodFiles()
	sceneOf(doc)["video"] = path
	files[path] = clip
	return doc, files
}

func TestASceneCanPlayAClip(t *testing.T) {
	for _, tc := range []struct {
		path, contentType string
		clip              []byte
	}{
		{"figures/claude/read.mp4", "video/mp4", mp4Clip},
		{"figures/claude/read.webm", "video/webm", webmClip},
	} {
		t.Run(tc.path, func(t *testing.T) {
			s, database := newTestStore(t)
			doc, files := filmedPack(tc.path, tc.clip)
			if err := importErr(t, s, doc, files); err != nil {
				t.Fatal(err)
			}
			if got := string(readPackFile(t, s, "magic", tc.path)); got != string(tc.clip) {
				t.Fatalf("clip = %q", got)
			}
			if got, ok := ContentType(tc.path); !ok || got != tc.contentType {
				t.Fatalf("content type = %q, %v", got, ok)
			}
			stored := asDoc(t, readPackFile(t, s, "magic", ManifestFile))
			want := map[string]any{
				"room": "lounge", "image": "figures/claude/read.png", "video": tc.path,
				"x": 100.0, "y": 200.0, "w": 300.0, "h": 500.0,
				"box": []any{120.0, 210.0, 380.0, 700.0},
			}
			if got := sceneOf(stored); !reflect.DeepEqual(got, want) {
				t.Fatalf("scene = %v, want %v", got, want)
			}
			expectNoTrace(t, s, database, "magic")
		})
	}
}

func TestASceneWithoutAClipStoresNone(t *testing.T) {
	if video, has := sceneOf(storedForm(t, manifestDoc()))["video"]; has {
		t.Fatalf("video = %v", video)
	}
	// An empty video is no video, as an empty room is no room.
	doc := manifestDoc()
	sceneOf(doc)["video"] = ""
	if video, has := sceneOf(storedForm(t, doc))["video"]; has {
		t.Fatalf("an empty video was kept: %v", video)
	}
}

func TestASpoiledClipRefusesTheWholeZip(t *testing.T) {
	s, database := newTestStore(t)
	const clip = "figures/claude/read.mp4"
	for _, tc := range []struct {
		name string
		pack func() (map[string]any, map[string][]byte)
	}{
		{"an mp4 named as the image", func() (map[string]any, map[string][]byte) {
			doc, files := manifestDoc(), goodFiles()
			sceneOf(doc)["image"] = clip
			delete(files, "figures/claude/read.png")
			files[clip] = mp4Clip
			return doc, files
		}},
		{"a webm named as a room", func() (map[string]any, map[string][]byte) {
			doc, files := manifestDoc(), goodFiles()
			doc["rooms"] = map[string]any{"lounge": "rooms/lounge.webm"}
			delete(files, "rooms/lounge.webp")
			files["rooms/lounge.webm"] = webmClip
			return doc, files
		}},
		{"an mp4 named as a sign", func() (map[string]any, map[string][]byte) {
			doc, files := manifestDoc(), goodFiles()
			characterOf(doc)["sign"] = "signs/mage.mp4"
			files["signs/mage.mp4"] = mp4Clip
			return doc, files
		}},
		{"a png named as the video", func() (map[string]any, map[string][]byte) {
			return filmedPack("figures/claude/moving.png", pngImage)
		}},
		{"the still named as the video", func() (map[string]any, map[string][]byte) {
			doc, files := manifestDoc(), goodFiles()
			sceneOf(doc)["video"] = "figures/claude/read.png"
			return doc, files
		}},
		{"a video of another type", func() (map[string]any, map[string][]byte) {
			return filmedPack("figures/claude/read.mov", mp4Clip)
		}},
		{"a capital extension", func() (map[string]any, map[string][]byte) {
			return filmedPack("figures/claude/read.MP4", mp4Clip)
		}},
		{"a path that walks", func() (map[string]any, map[string][]byte) {
			return filmedPack("../read.mp4", mp4Clip)
		}},
		{"a video that is no string", func() (map[string]any, map[string][]byte) {
			doc, files := filmedPack(clip, mp4Clip)
			sceneOf(doc)["video"] = []any{clip}
			return doc, files
		}},
		{"an mp4 that is a png", func() (map[string]any, map[string][]byte) { return filmedPack(clip, pngImage) }},
		{"an mp4 that is a webm", func() (map[string]any, map[string][]byte) { return filmedPack(clip, webmClip) }},
		{"a webm that is an mp4", func() (map[string]any, map[string][]byte) {
			return filmedPack("figures/claude/read.webm", mp4Clip)
		}},
		{"an mp4 holding HTML", func() (map[string]any, map[string][]byte) {
			return filmedPack(clip, []byte("<!DOCTYPE html><script>alert(1)</script>"))
		}},
		{"an empty clip", func() (map[string]any, map[string][]byte) { return filmedPack(clip, nil) }},
		{"a video and frames", func() (map[string]any, map[string][]byte) {
			doc, files := filmedPack(clip, mp4Clip)
			sceneOf(doc)["frames"] = map[string]any{"count": 2}
			return doc, files
		}},
		{"a video named but not in the zip", func() (map[string]any, map[string][]byte) {
			doc, files := filmedPack(clip, mp4Clip)
			delete(files, clip)
			return doc, files
		}},
		{"an mp4 in the zip but not named", func() (map[string]any, map[string][]byte) {
			files := goodFiles()
			files[clip] = mp4Clip
			return manifestDoc(), files
		}},
		{"a webm in the zip but not named", func() (map[string]any, map[string][]byte) {
			files := goodFiles()
			files["extra.webm"] = webmClip
			return manifestDoc(), files
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, files := tc.pack()
			expectRefused(t, importErr(t, s, doc, files), http.StatusBadRequest)
			expectNoTrace(t, s, database)
		})
	}
}

// A scene can also play a clip once when it is clicked: named by the scene's
// clickVideo, under the same rules as its video, with or without one.

// clickedPack is a good pack whose scene plays the clip at path on a click,
// and its files.
func clickedPack(path string, clip []byte) (map[string]any, map[string][]byte) {
	doc, files := manifestDoc(), goodFiles()
	sceneOf(doc)["clickVideo"] = path
	files[path] = clip
	return doc, files
}

func TestASceneCanPlayAClipOnAClick(t *testing.T) {
	for _, tc := range []struct {
		name  string
		video bool
		path  string
		clip  []byte
	}{
		{"an mp4 with no loop", false, "figures/claude/read-click.mp4", mp4Clip},
		{"a webm with no loop", false, "figures/claude/read-click.webm", webmClip},
		{"an mp4 after a loop", true, "figures/claude/read-click.mp4", mp4Clip},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, database := newTestStore(t)
			doc, files := clickedPack(tc.path, tc.clip)
			want := map[string]any{
				"room": "lounge", "image": "figures/claude/read.png", "clickVideo": tc.path,
				"x": 100.0, "y": 200.0, "w": 300.0, "h": 500.0,
				"box": []any{120.0, 210.0, 380.0, 700.0},
			}
			if tc.video {
				sceneOf(doc)["video"] = "figures/claude/read.mp4"
				files["figures/claude/read.mp4"] = mp4Clip
				want["video"] = "figures/claude/read.mp4"
			}
			if err := importErr(t, s, doc, files); err != nil {
				t.Fatal(err)
			}
			if got := string(readPackFile(t, s, "magic", tc.path)); got != string(tc.clip) {
				t.Fatalf("clip = %q", got)
			}
			stored := asDoc(t, readPackFile(t, s, "magic", ManifestFile))
			if got := sceneOf(stored); !reflect.DeepEqual(got, want) {
				t.Fatalf("scene = %v, want %v", got, want)
			}
			expectNoTrace(t, s, database, "magic")
		})
	}
}

func TestASceneWithoutAClickClipStoresNone(t *testing.T) {
	if clip, has := sceneOf(storedForm(t, manifestDoc()))["clickVideo"]; has {
		t.Fatalf("clickVideo = %v", clip)
	}
	doc := manifestDoc()
	sceneOf(doc)["clickVideo"] = ""
	if clip, has := sceneOf(storedForm(t, doc))["clickVideo"]; has {
		t.Fatalf("an empty clickVideo was kept: %v", clip)
	}
}

func TestASpoiledClickClipRefusesTheWholeZip(t *testing.T) {
	s, database := newTestStore(t)
	const clip = "figures/claude/read-click.mp4"
	for _, tc := range []struct {
		name string
		pack func() (map[string]any, map[string][]byte)
	}{
		{"a png named as the click video", func() (map[string]any, map[string][]byte) {
			return clickedPack("figures/claude/clicked.png", pngImage)
		}},
		{"the still named as the click video", func() (map[string]any, map[string][]byte) {
			doc, files := manifestDoc(), goodFiles()
			sceneOf(doc)["clickVideo"] = "figures/claude/read.png"
			return doc, files
		}},
		{"a click video of another type", func() (map[string]any, map[string][]byte) {
			return clickedPack("figures/claude/read-click.mov", mp4Clip)
		}},
		{"a capital extension", func() (map[string]any, map[string][]byte) {
			return clickedPack("figures/claude/read-click.MP4", mp4Clip)
		}},
		{"a path that walks", func() (map[string]any, map[string][]byte) {
			return clickedPack("../read-click.mp4", mp4Clip)
		}},
		{"a click video that is no string", func() (map[string]any, map[string][]byte) {
			doc, files := clickedPack(clip, mp4Clip)
			sceneOf(doc)["clickVideo"] = []any{clip}
			return doc, files
		}},
		{"an mp4 that is a png", func() (map[string]any, map[string][]byte) { return clickedPack(clip, pngImage) }},
		{"a webm that is an mp4", func() (map[string]any, map[string][]byte) {
			return clickedPack("figures/claude/read-click.webm", mp4Clip)
		}},
		{"an mp4 holding HTML", func() (map[string]any, map[string][]byte) {
			return clickedPack(clip, []byte("<!DOCTYPE html><script>alert(1)</script>"))
		}},
		{"an empty clip", func() (map[string]any, map[string][]byte) { return clickedPack(clip, nil) }},
		{"a click video and frames", func() (map[string]any, map[string][]byte) {
			doc, files := clickedPack(clip, mp4Clip)
			sceneOf(doc)["frames"] = map[string]any{"count": 2}
			return doc, files
		}},
		{"a click video named but not in the zip", func() (map[string]any, map[string][]byte) {
			doc, files := clickedPack(clip, mp4Clip)
			delete(files, clip)
			return doc, files
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, files := tc.pack()
			expectRefused(t, importErr(t, s, doc, files), http.StatusBadRequest)
			expectNoTrace(t, s, database)
		})
	}
}
