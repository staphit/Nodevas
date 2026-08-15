package docx

import (
	"archive/zip"
	"bytes"
	"fmt"
	"strings"
	"testing"

	"nodevas/internal/document"
)

func zipPayload(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	var out bytes.Buffer
	writer := zip.NewWriter(&out)
	for name, contents := range entries {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(contents); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// roundTripDOCX is the conversion a .docx page goes through every time it is
// opened and saved: Markdown → Word → Markdown.
func roundTripDOCX(t *testing.T, source string, sink MediaSink) string {
	t.Helper()
	data, err := RenderDOCX(document.Parse(source), document.Options{Assets: func(url string) (document.Asset, bool) {
		return document.Asset{
			Data:   []byte("\x89PNG\r\n\x1a\n fake"),
			MIME:   "image/png",
			Name:   "shot.png",
			Width:  120,
			Height: 60,
		}, true
	}})
	if err != nil {
		t.Fatalf("RenderDOCX: %v", err)
	}
	doc, err := ReadDOCX(data, sink)
	if err != nil {
		t.Fatalf("ReadDOCX: %v", err)
	}
	return document.RenderMarkdown(doc)
}

func TestDOCXRoundTripKeepsStructure(t *testing.T) {
	source := strings.Join([]string{
		"# Title",
		"",
		"Intro with **bold**, *italic*, ~~struck~~ and `code`.",
		"",
		"## Section",
		"",
		"- first item",
		"- second item with [link](https://example.com)",
		"  - nested item",
		"",
		"3. three",
		"4. four",
		"",
		"- [ ] open task",
		"- [x] done task",
		"",
		"> quoted line",
		"",
		"| Name | Count | Note |",
		"| :--- | ----: | :--: |",
		"| a    |     1 | yes  |",
		"| b    |    22 | no   |",
		"",
		"```",
		`fmt.Println("hi")`,
		"```",
		"",
	}, "\n")

	result := roundTripDOCX(t, source, nil)
	doc := document.Parse(result)

	headings := map[int]string{}
	var lists []*document.List
	var tables []*document.Table
	var codes []*document.CodeBlock
	var quotes []*document.BlockQuote
	for _, block := range doc.Blocks {
		switch node := block.(type) {
		case *document.Heading:
			headings[node.Level] = document.PlainText(node.Inlines)
		case *document.List:
			lists = append(lists, node)
		case *document.Table:
			tables = append(tables, node)
		case *document.CodeBlock:
			codes = append(codes, node)
		case *document.BlockQuote:
			quotes = append(quotes, node)
		}
	}

	if headings[1] != "Title" || headings[2] != "Section" {
		t.Errorf("headings = %v\n---\n%s", headings, result)
	}
	for _, want := range []string{"**bold**", "*italic*", "~~struck~~", "`code`",
		"[link](https://example.com)"} {
		if !strings.Contains(result, want) {
			t.Errorf("round trip lost %q\n---\n%s", want, result)
		}
	}
	if len(lists) != 3 {
		t.Fatalf("lists = %d, want bullet + ordered + task\n---\n%s", len(lists), result)
	}
	if len(lists[0].Items) != 2 {
		t.Errorf("bullet items = %d, want 2\n---\n%s", len(lists[0].Items), result)
	}
	if len(lists[0].Items[1].Blocks) < 2 {
		t.Errorf("nested list was flattened\n---\n%s", result)
	}
	if !lists[1].Ordered || lists[1].Start != 3 {
		t.Errorf("ordered list = %+v, want start 3\n---\n%s", lists[1], result)
	}
	if !lists[2].Items[0].Task || lists[2].Items[0].Checked {
		t.Errorf("first task = %+v, want unchecked task\n---\n%s", lists[2].Items[0], result)
	}
	if !lists[2].Items[1].Checked {
		t.Errorf("second task should be checked\n---\n%s", result)
	}
	if len(quotes) != 1 || !strings.Contains(document.PlainText(quotes[0].Blocks[0].(*document.Paragraph).Inlines), "quoted line") {
		t.Errorf("quote did not survive\n---\n%s", result)
	}
	if len(codes) != 1 || !strings.Contains(codes[0].Code, `fmt.Println("hi")`) {
		t.Errorf("code block = %+v\n---\n%s", codes, result)
	}
	if len(tables) != 1 {
		t.Fatalf("tables = %d, want 1\n---\n%s", len(tables), result)
	}
	table := tables[0]
	if got := document.PlainText(table.Head[0].Inlines); got != "Name" {
		t.Errorf("header cell = %q", got)
	}
	if len(table.Rows) != 2 || document.PlainText(table.Rows[1][1].Inlines) != "22" {
		t.Errorf("table rows = %+v\n---\n%s", table.Rows, result)
	}
	if table.Align[1] != document.AlignRight || table.Align[2] != document.AlignCenter {
		t.Errorf("alignment = %v, want default/right/center", table.Align)
	}
}

func TestDOCXRoundTripStoresImages(t *testing.T) {
	var storedName string
	var storedBytes []byte
	result := roundTripDOCX(t, "![封面](/api/nodes/n1/files/shot.png)\n",
		func(name string, data []byte) (string, error) {
			storedName, storedBytes = name, data
			return "/api/nodes/n1/files/" + name, nil
		})
	if storedName != "image1.png" || len(storedBytes) == 0 {
		t.Fatalf("image was not handed to the sink: %q (%d bytes)", storedName, len(storedBytes))
	}
	if !strings.Contains(result, "![封面](/api/nodes/n1/files/image1.png)") {
		t.Errorf("image reference = %q", result)
	}
}

func TestDOCXReadWithoutSinkKeepsAltText(t *testing.T) {
	result := roundTripDOCX(t, "before\n\n![封面](/api/nodes/n1/files/shot.png)\n\nafter\n", nil)
	if strings.Contains(result, "![") {
		t.Errorf("image should degrade without a sink: %q", result)
	}
	for _, want := range []string{"before", "after"} {
		if !strings.Contains(result, want) {
			t.Errorf("surrounding text lost: %q", result)
		}
	}
}

func TestReadDOCXRejectsNonWordInput(t *testing.T) {
	if _, err := ReadDOCX([]byte("not a zip"), nil); err == nil {
		t.Error("a non-zip payload should fail")
	}
}

func TestValidateDOCXArchiveResourceLimits(t *testing.T) {
	file := func(name string, size, compressed uint64) *zip.File {
		return &zip.File{FileHeader: zip.FileHeader{
			Name:               name,
			UncompressedSize64: size,
			CompressedSize64:   compressed,
		}}
	}

	t.Run("entry count", func(t *testing.T) {
		files := make([]*zip.File, maxDOCXEntries+1)
		for index := range files {
			files[index] = file(fmt.Sprintf("custom/%d.xml", index), 0, 0)
		}
		if err := validateDOCXArchive(files); err == nil || !strings.Contains(err.Error(), "too many entries") {
			t.Fatalf("entry count error = %v", err)
		}
	})

	t.Run("total expansion", func(t *testing.T) {
		files := []*zip.File{
			file("custom/one.bin", maxDOCXPartBytes, maxDOCXPartBytes),
			file("custom/two.bin", maxDOCXPartBytes, maxDOCXPartBytes),
			file("custom/three.bin", 1, 1),
		}
		if err := validateDOCXArchive(files); err == nil || !strings.Contains(err.Error(), "expands beyond") {
			t.Fatalf("total expansion error = %v", err)
		}
	})

	t.Run("image count", func(t *testing.T) {
		files := make([]*zip.File, maxDOCXImages+1)
		for index := range files {
			files[index] = file(fmt.Sprintf("word/media/%d.png", index), 1, 1)
		}
		if err := validateDOCXArchive(files); err == nil || !strings.Contains(err.Error(), "too many images") {
			t.Fatalf("image count error = %v", err)
		}
	})

	t.Run("image size", func(t *testing.T) {
		files := []*zip.File{file("word/media/large.png", maxDOCXImageBytes+1, maxDOCXImageBytes+1)}
		if err := validateDOCXArchive(files); err == nil || !strings.Contains(err.Error(), "image is too large") {
			t.Fatalf("image size error = %v", err)
		}
	})
}

func TestRenderMarkdownRoundTripsThroughParse(t *testing.T) {
	source := strings.Join([]string{
		"# Title",
		"",
		"document.Text with **bold** and a [link](https://example.com).",
		"",
		"- one",
		"  - deep",
		"- [x] done",
		"",
		"| a | b |",
		"| --- | ---: |",
		"| 1 | 2 |",
		"",
		"> quote",
		"",
		"```go",
		"code()",
		"```",
		"",
	}, "\n")
	once := document.RenderMarkdown(document.Parse(source))
	twice := document.RenderMarkdown(document.Parse(once))
	if once != twice {
		t.Errorf("markdown writer is not idempotent:\n--- once ---\n%s\n--- twice ---\n%s", once, twice)
	}
}

func TestRenderMarkdownEscapesText(t *testing.T) {
	doc := &document.Doc{Blocks: []document.Block{
		&document.Paragraph{Inlines: []document.Inline{&document.Text{Value: "a * b _ c [d] | e"}}},
	}}
	rendered := document.RenderMarkdown(doc)
	if document.PlainText(document.Parse(rendered).Blocks[0].(*document.Paragraph).Inlines) != "a * b _ c [d] | e" {
		t.Errorf("escaping did not survive a re-parse: %q", rendered)
	}
}
