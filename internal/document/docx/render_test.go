package docx

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nodevas/internal/document"
)

func TestRenderDOCXPackage(t *testing.T) {
	data, err := RenderDOCX(parseSample(t), document.Options{
		Title:    "文本-2",
		Modified: time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("RenderDOCX: %v", err)
	}
	for _, part := range []string{
		"[Content_Types].xml",
		"_rels/.rels",
		"word/document.xml",
		"word/styles.xml",
		"word/numbering.xml",
		"word/_rels/document.xml.rels",
		"docProps/core.xml",
	} {
		body := readPart(t, data, part)
		if err := xml.Unmarshal([]byte(body), new(struct {
			XMLName xml.Name
			Inner   []byte `xml:",innerxml"`
		})); err != nil {
			t.Errorf("%s is not well-formed XML: %v", part, err)
		}
	}
	documentXML := readPart(t, data, "word/document.xml")
	for _, want := range []string{
		`<w:pStyle w:val="Heading1"/>`,
		`<w:pStyle w:val="Heading2"/>`,
		`<w:b/>`,
		`<w:i/>`,
		`<w:strike/>`,
		`<w:rStyle w:val="CodeChar"/>`,
		`<w:numId w:val="1"/>`,
		`<w:tbl>`,
		`<w:tblHeader/>`,
		`<w:hyperlink r:id="rId3"`,
		`☐ `,
		`☑ `,
	} {
		if !strings.Contains(documentXML, want) {
			t.Errorf("document.xml is missing %q", want)
		}
	}
	rels := readPart(t, data, "word/_rels/document.xml.rels")
	if !strings.Contains(rels, `Target="https://example.com" TargetMode="External"`) {
		t.Errorf("hyperlink relationship missing:\n%s", rels)
	}
	numbering := readPart(t, data, "word/numbering.xml")
	if !strings.Contains(numbering, `<w:startOverride w:val="3"/>`) {
		t.Errorf("ordered list start was not carried over:\n%s", numbering)
	}
	core := readPart(t, data, "docProps/core.xml")
	if !strings.Contains(core, "<dc:title>文本-2</dc:title>") {
		t.Errorf("core properties missing the title:\n%s", core)
	}
}

// Word refuses a package with a dangling relationship, style or numbering
// reference, so every id the document mentions must exist in its part.
func TestRenderDOCXReferencesResolve(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n fake")
	data, err := RenderDOCX(parseSample(t), document.Options{
		Assets: func(string) (document.Asset, bool) {
			return document.Asset{Data: png, MIME: "image/png", Name: "shot.png", Width: 80, Height: 40}, true
		},
	})
	if err != nil {
		t.Fatalf("RenderDOCX: %v", err)
	}
	relations := attributeValues(t, readPart(t, data, "word/_rels/document.xml.rels"), "Relationship", "Id")
	styles := attributeValues(t, readPart(t, data, "word/styles.xml"), "style", "styleId")
	numbers := attributeValues(t, readPart(t, data, "word/numbering.xml"), "num", "numId")
	targets := attributeValues(t, readPart(t, data, "word/_rels/document.xml.rels"), "Relationship", "Target")

	decoder := xml.NewDecoder(strings.NewReader(readPart(t, data, "word/document.xml")))
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("scan document.xml: %v", err)
		}
		element, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		for _, attr := range element.Attr {
			switch {
			case attr.Name.Local == "id" && attr.Name.Space != "" && element.Name.Local == "hyperlink",
				attr.Name.Local == "embed":
				if !relations[attr.Value] {
					t.Errorf("<%s> points at unknown relationship %q", element.Name.Local, attr.Value)
				}
			}
		}
		if attr, ok := attributeOf(element, "val"); ok {
			switch element.Name.Local {
			case "pStyle", "rStyle", "tblStyle":
				if !styles[attr] {
					t.Errorf("<%s> uses undefined style %q", element.Name.Local, attr)
				}
			case "numId":
				if !numbers[attr] {
					t.Errorf("numbering instance %q is not defined", attr)
				}
			}
		}
	}
	for target := range targets {
		if !strings.HasPrefix(target, "media/") {
			continue
		}
		readPart(t, data, "word/"+target) // fails the test when missing
	}
}

func TestRenderDOCXEmbedsImage(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n fake")
	data, err := RenderDOCX(document.Parse("![shot](/api/nodes/n1/files/shot.png)"), document.Options{
		Assets: func(url string) (document.Asset, bool) {
			if url != "/api/nodes/n1/files/shot.png" {
				return document.Asset{}, false
			}
			return document.Asset{Data: png, MIME: "image/png", Name: "shot.png", Width: 800, Height: 400}, true
		},
	})
	if err != nil {
		t.Fatalf("RenderDOCX: %v", err)
	}
	documentXML := readPart(t, data, "word/document.xml")
	if !strings.Contains(documentXML, "<w:drawing>") {
		t.Fatalf("image was not embedded:\n%s", documentXML)
	}
	if media := readPart(t, data, "word/media/image1.png"); media != string(png) {
		t.Errorf("media bytes = %q", media)
	}
	if types := readPart(t, data, "[Content_Types].xml"); !strings.Contains(types,
		`<Default Extension="png" ContentType="image/png"/>`) {
		t.Errorf("png content type missing:\n%s", types)
	}
}

func TestRenderDOCXFallsBackToLinkWithoutAsset(t *testing.T) {
	data, err := RenderDOCX(document.Parse("![shot](https://example.com/x.png)"), document.Options{})
	if err != nil {
		t.Fatalf("RenderDOCX: %v", err)
	}
	documentXML := readPart(t, data, "word/document.xml")
	if strings.Contains(documentXML, "<w:drawing>") {
		t.Error("unresolvable image should not produce a drawing")
	}
	if !strings.Contains(documentXML, "shot") {
		t.Errorf("alt text was dropped:\n%s", documentXML)
	}
}

func TestRenderDOCXDropsControlCharacters(t *testing.T) {
	data, err := RenderDOCX(document.Parse("bad\x07char"), document.Options{})
	if err != nil {
		t.Fatalf("RenderDOCX: %v", err)
	}
	documentXML := readPart(t, data, "word/document.xml")
	if strings.Contains(documentXML, "\x07") {
		t.Error("control character survived into document.xml")
	}
	if !strings.Contains(documentXML, "badchar") {
		t.Errorf("text was mangled:\n%s", documentXML)
	}
}

// parseSample parses the fixture shared with the core document tests.
func parseSample(t *testing.T) *document.Doc {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "testdata", "sample.md"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	doc := document.Parse(string(raw))
	if len(doc.Blocks) == 0 {
		t.Fatal("sample parsed to nothing")
	}
	return doc
}

func readPart(t *testing.T, data []byte, name string) string {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("open docx: %v", err)
	}
	for _, file := range reader.File {
		if file.Name != name {
			continue
		}
		entry, err := file.Open()
		if err != nil {
			t.Fatalf("open %s: %v", name, err)
		}
		defer entry.Close()
		body, err := io.ReadAll(entry)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return string(body)
	}
	t.Fatalf("%s missing from package", name)
	return ""
}

// Word refuses a package with a dangling relationship, style or numbering
// reference, so every id the document mentions must exist in its part.
func attributeOf(element xml.StartElement, name string) (string, bool) {
	for _, attr := range element.Attr {
		if attr.Name.Local == name {
			return attr.Value, true
		}
	}
	return "", false
}

func attributeValues(t *testing.T, source, element, attribute string) map[string]bool {
	t.Helper()
	found := map[string]bool{}
	decoder := xml.NewDecoder(strings.NewReader(source))
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return found
		}
		if err != nil {
			t.Fatalf("scan %s: %v", element, err)
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != element {
			continue
		}
		if value, ok := attributeOf(start, attribute); ok {
			found[value] = true
		}
	}
}
