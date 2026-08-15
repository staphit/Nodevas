// Package export turns project Markdown into shareable documents: plain
// text, standalone HTML and Word (.docx). The Markdown is parsed once into
// the block model below, and every renderer walks that same model so the
// formats never drift apart.
package document

import (
	"strings"
	"time"
)

// Doc is a parsed Markdown document.
type Doc struct {
	Blocks []Block
}

// Block is a top-level element (heading, paragraph, list, …).
type Block interface{ isBlock() }

// Heading is an ATX (`## x`) or setext heading, Level 1..6.
type Heading struct {
	Level   int
	Inlines []Inline
}

// Paragraph is a run of text separated by blank lines.
type Paragraph struct {
	Inlines []Inline
}

// CodeBlock is a fenced code block; Lang may be empty.
type CodeBlock struct {
	Lang string
	Code string
}

// BlockQuote is a `>` quoted region, which may hold any other block.
type BlockQuote struct {
	Blocks []Block
}

// List is a bullet or ordered list. Start is the first number of an ordered
// list, so `3.` keeps counting from three.
type List struct {
	Ordered bool
	Start   int
	Items   []*ListItem
}

// ListItem holds the blocks of one entry. Task marks a `- [ ]` checkbox.
type ListItem struct {
	Task    bool
	Checked bool
	Blocks  []Block
}

// Align is a table column alignment.
type Align int

const (
	AlignDefault Align = iota
	AlignLeft
	AlignCenter
	AlignRight
)

// TableCell is one cell's inline content.
type TableCell struct {
	Inlines []Inline
}

// Table is a GFM pipe table. Head is the (single) header row.
type Table struct {
	Align []Align
	Head  []TableCell
	Rows  [][]TableCell
}

// Rule is a thematic break (`---`).
type Rule struct{}

func (*Heading) isBlock()    {}
func (*Paragraph) isBlock()  {}
func (*CodeBlock) isBlock()  {}
func (*BlockQuote) isBlock() {}
func (*List) isBlock()       {}
func (*Table) isBlock()      {}
func (*Rule) isBlock()       {}

// Inline is a span-level element inside a block.
type Inline interface{ isInline() }

// Text is literal text with all escapes already resolved.
type Text struct{ Value string }

// Strong is `**bold**`.
type Strong struct{ Kids []Inline }

// Emphasis is `*italic*`.
type Emphasis struct{ Kids []Inline }

// Strike is `~~struck~~`.
type Strike struct{ Kids []Inline }

// CodeSpan is a backtick-delimited inline code span.
type CodeSpan struct{ Value string }

// Link is `[text](url "title")` or an autolink.
type Link struct {
	URL   string
	Title string
	Kids  []Inline
}

// Image is `![alt](url)`.
type Image struct {
	URL   string
	Alt   string
	Title string
}

// LineBreak is a newline inside a paragraph; Hard marks an explicit
// two-space or backslash break.
type LineBreak struct{ Hard bool }

func (*Text) isInline()      {}
func (*Strong) isInline()    {}
func (*Emphasis) isInline()  {}
func (*Strike) isInline()    {}
func (*CodeSpan) isInline()  {}
func (*Link) isInline()      {}
func (*Image) isInline()     {}
func (*LineBreak) isInline() {}

// Asset is a resolved image the renderers can embed instead of linking.
type Asset struct {
	Data   []byte
	MIME   string
	Name   string
	Width  int // pixels; 0 when unknown
	Height int
}

// AssetResolver maps a document image URL to its bytes. It returns false for
// URLs that cannot be embedded (remote images, missing attachments), and the
// renderer falls back to a plain link.
type AssetResolver func(url string) (Asset, bool)

// Options tune the renderers that produce a whole file.
type Options struct {
	// Title is the document title (HTML <title>, docx core properties).
	Title string
	// Assets embeds images when set.
	Assets AssetResolver
	// Modified stamps the generated file; zero means "now".
	Modified time.Time
}

// ModifiedOrNow is the timestamp to stamp a generated file with.
func (o Options) ModifiedOrNow() time.Time {
	if o.Modified.IsZero() {
		return time.Now()
	}
	return o.Modified
}

// Resolve looks up an image's bytes, reporting false when it cannot be
// embedded and the renderer must fall back to a link.
func (o Options) Resolve(url string) (Asset, bool) {
	if o.Assets == nil {
		return Asset{}, false
	}
	return o.Assets(url)
}

// AlignAt is the alignment of a table's column, defaulting for columns the
// header row did not specify.
func AlignAt(aligns []Align, index int) Align {
	if index < len(aligns) {
		return aligns[index]
	}
	return AlignDefault
}

// safeURLImageTypes are the image subtypes a `data:` URL may carry. Anything
// else (svg+xml above all, which can script) stays out of an export.
var safeURLImageTypes = []string{"png", "jpeg", "gif", "webp"}

// stripURLControls removes every character an HTML parser drops from inside an
// attribute value before it resolves the URL: NUL and the C0 controls, notably
// TAB, LF and CR. Without this, `java&#9;script:alert(1)` survives both the
// scheme check below and html.EscapeString (which leaves control characters
// alone) and re-forms as `javascript:` in the browser. Spaces are kept inside
// the URL — browsers do not strip them, so they cannot rebuild a scheme — and
// only trimmed at the ends.
func stripURLControls(raw string) string {
	var b strings.Builder
	for _, r := range raw {
		if r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

// SafeURL blocks script-bearing schemes from an exported file. Every renderer
// that writes a URL into its output runs it through here. The exported HTML is
// a standalone file with no CSP, so this is the only gate: the scheme must be
// on the allowlist, or the URL must be relative (a path, query or fragment,
// which is how attachments are referenced). Everything else becomes "".
//
// The returned string is the control-character-stripped form, never the raw
// input, so the browser resolves exactly the string that was checked.
func SafeURL(raw string) string {
	url := stripURLControls(raw)
	if url == "" {
		return ""
	}
	scheme, rest, hasScheme := splitURLScheme(url)
	if !hasScheme {
		// Relative: `/path`, `./x`, `#anchor`, `?q=1`, `img.png`.
		return url
	}
	switch scheme {
	case "http", "https", "mailto":
		return url
	case "data":
		if safeDataImage(rest) {
			return url
		}
	}
	return ""
}

// splitURLScheme reports the lowercased scheme of an absolute URL. A colon
// reached before any `/`, `?` or `#` opens a scheme; a colon after one of them
// belongs to a relative reference (say `./a:b/c`) and is left alone. A prefix
// that is not a syntactically valid scheme is still reported as one, so that
// the caller's allowlist rejects it rather than the browser and this function
// disagreeing about where the scheme ends.
func splitURLScheme(url string) (scheme, rest string, ok bool) {
	for index, r := range url {
		switch r {
		case ':':
			return strings.ToLower(url[:index]), url[index+1:], true
		case '/', '?', '#':
			return "", "", false
		}
	}
	return "", "", false
}

// safeDataImage accepts the body of a `data:` URL only when it declares one of
// the raster image types, e.g. `image/png;base64,…`.
func safeDataImage(body string) bool {
	lowered := strings.ToLower(body)
	const prefix = "image/"
	if !strings.HasPrefix(lowered, prefix) {
		return false
	}
	subtype := lowered[len(prefix):]
	if cut := strings.IndexAny(subtype, ";,"); cut >= 0 {
		subtype = subtype[:cut]
	} else {
		return false // no payload separator: not a usable data URL
	}
	for _, allowed := range safeURLImageTypes {
		if subtype == allowed {
			return true
		}
	}
	return false
}
