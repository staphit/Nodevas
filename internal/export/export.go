package export

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"nodevas/internal/document"
	"nodevas/internal/document/html"
	"nodevas/internal/engine"
	"nodevas/internal/store"
)

// Document export: the editor's Markdown is assembled for the requested
// scope, then rendered to the requested format. The client may pass the
// unsaved buffer so an export always matches what is on screen.

const (
	maxExportMarkdown = 32 << 20
)

type Format struct {
	Extension string
	Mime      string
}

var Formats = map[string]Format{
	"md":   {".md", "text/markdown; charset=utf-8"},
	"txt":  {".txt", "text/plain; charset=utf-8"},
	"html": {".html", "text/html; charset=utf-8"},
	"docx": {".docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
}

type Request struct {
	Format string `json:"format"`
	Scope  string `json:"scope"`
	NodeID string `json:"nodeId,omitempty"`
	PageID string `json:"pageId,omitempty"`
	// Content is the unsaved editor buffer for NodeID/PageID; nil means
	// "read the file from disk".
	Content *string `json:"content,omitempty"`
}

// ---------- assembling the source markdown ----------

func BuildMarkdown(st *store.Store, request Request) (string, string, error) {
	graph, _, err := st.LoadGraph()
	if err != nil {
		return "", "", err
	}
	switch strings.ToLower(strings.TrimSpace(request.Scope)) {
	case "", "page":
		return exportPage(st, graph, request)
	case "node":
		return exportNode(st, graph, request)
	case "project":
		return exportProject(st, graph, request)
	default:
		return "", "", fmt.Errorf("unsupported scope %q", request.Scope)
	}
}

func exportPage(st *store.Store, graph *engine.Graph, request Request) (string, string, error) {
	node := graph.NodeByID(request.NodeID)
	if node == nil {
		return "", "", fmt.Errorf("node %q not found", request.NodeID)
	}
	if isMainPage(request.PageID) {
		body, err := nodeBody(st, node.ID, request)
		if err != nil {
			return "", "", err
		}
		title := nodeTitle(node)
		return exportSection(title, body, 1), title, nil
	}
	pages, err := st.ListNodePages(node.ID)
	if err != nil {
		return "", "", err
	}
	page, ok := FindPage(pages, request.PageID)
	if !ok {
		return "", "", fmt.Errorf("page %q not found", request.PageID)
	}
	body, err := pageBody(st, node.ID, page.ID, request)
	if err != nil {
		return "", "", err
	}
	title := nodeTitle(node) + " - " + page.Title
	return exportSection(page.Title, body, 1), title, nil
}

func exportNode(st *store.Store, graph *engine.Graph, request Request) (string, string, error) {
	node := graph.NodeByID(request.NodeID)
	if node == nil {
		return "", "", fmt.Errorf("node %q not found", request.NodeID)
	}
	title := nodeTitle(node)
	sections, err := nodeSections(st, node, request, 1)
	if err != nil {
		return "", "", err
	}
	return strings.Join(sections, "\n\n"), title, nil
}

func exportProject(st *store.Store, graph *engine.Graph, request Request) (string, string, error) {
	title := filepath.Base(st.Root())
	sections := []string{"# " + title}
	// The wiring goes first: without it the export is a pile of documents with
	// no way to tell what leads to what.
	if relations := RelationSection(graph, 2); relations != "" {
		sections = append(sections, relations)
	}
	total := len(title)
	for _, node := range graph.Nodes {
		rendered, err := nodeSections(st, node, request, 2)
		if err != nil {
			// A node whose file is missing must not sink the whole export.
			continue
		}
		for _, section := range rendered {
			total += len(section)
			if total > maxExportMarkdown {
				return "", "", errors.New("project is too large to export as one document")
			}
			sections = append(sections, section)
		}
	}
	return strings.Join(sections, "\n\n"), title, nil
}

// nodeSections renders one node and its subpages, the node at baseLevel and
// each subpage one level below it.
func nodeSections(
	st *store.Store,
	node *engine.Node,
	request Request,
	baseLevel int,
) ([]string, error) {
	body, err := nodeBody(st, node.ID, request)
	if err != nil {
		return nil, err
	}
	sections := []string{exportSection(nodeTitle(node), body, baseLevel)}
	pages, err := st.ListNodePages(node.ID)
	if err != nil {
		return sections, nil
	}
	for _, page := range pages {
		body, err := pageBody(st, node.ID, page.ID, request)
		if err != nil {
			continue
		}
		sections = append(sections, exportSection(page.Title, body, baseLevel+1))
	}
	return sections, nil
}

// nodeBody prefers the unsaved buffer the client sent for this document.
func nodeBody(st *store.Store, nodeID string, request Request) (string, error) {
	if request.Content != nil && request.NodeID == nodeID && isMainPage(request.PageID) {
		return *request.Content, nil
	}
	raw, _, err := st.LoadNodeContent(nodeID)
	if err != nil {
		return "", err
	}
	parsed, err := engine.ParseNodeFile([]byte(raw))
	if err != nil {
		return raw, nil
	}
	return parsed.Body, nil
}

// pageBody returns the page as Markdown, whatever it is stored as: an HTML
// page would otherwise land in the export as literal tags.
func pageBody(st *store.Store, nodeID, pageID string, request Request) (string, error) {
	page, content, _, err := st.LoadNodePage(nodeID, pageID)
	if err != nil {
		return "", err
	}
	format := page.Format
	if request.Content != nil && request.NodeID == nodeID && request.PageID == pageID {
		content = *request.Content
	}
	if format != store.PageFormatHTML {
		return content, nil
	}
	doc, err := html.ReadHTML(content)
	if err != nil {
		return content, nil
	}
	return document.RenderMarkdown(doc), nil
}

func isMainPage(pageID string) bool {
	return pageID == "" || pageID == "main"
}

func FindPage(pages []store.NodePageInfo, id string) (store.NodePageInfo, bool) {
	for _, page := range pages {
		if page.ID == id {
			return page, true
		}
	}
	return store.NodePageInfo{}, false
}

func nodeTitle(node *engine.Node) string {
	if title := strings.TrimSpace(node.Title); title != "" {
		return title
	}
	return node.ID
}

// exportSection puts a document under one heading at the given level: an
// existing top heading is reused, otherwise the title is prepended.
func exportSection(title, body string, level int) string {
	body = strings.TrimSpace(body)
	if !startsWithTopHeading(body) {
		heading := strings.TrimSpace(title)
		if heading == "" {
			heading = "未命名"
		}
		body = "# " + heading + "\n\n" + body
	}
	return strings.TrimSpace(ShiftHeadings(body, level-1))
}

var (
	headingPattern  = regexp.MustCompile(`^ {0,3}(#{1,6})(\s|$)`)
	topHeadingFirst = regexp.MustCompile(`^ {0,3}#(\s|$)`)
)

func startsWithTopHeading(body string) bool {
	for _, line := range strings.Split(body, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		return topHeadingFirst.MatchString(line)
	}
	return false
}

// ShiftHeadings pushes every heading down by `by` levels so a document can be
// nested under another one. Fenced code is left alone.
func ShiftHeadings(source string, by int) string {
	if by <= 0 {
		return source
	}
	lines := strings.Split(source, "\n")
	fence := ""
	for index, line := range lines {
		trimmed := strings.TrimLeft(line, " ")
		if fence != "" {
			if strings.HasPrefix(trimmed, fence) {
				fence = ""
			}
			continue
		}
		switch {
		case strings.HasPrefix(trimmed, "```"):
			fence = "```"
			continue
		case strings.HasPrefix(trimmed, "~~~"):
			fence = "~~~"
			continue
		}
		match := headingPattern.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		level := min(len(match[1])+by, 6)
		prefix := strings.Index(line, "#")
		lines[index] = line[:prefix] + strings.Repeat("#", level) + line[prefix+len(match[1]):]
	}
	return strings.Join(lines, "\n")
}

var exportNameBadChars = regexp.MustCompile(`[^\p{L}\p{N} ._()\-\[\]]+`)

func FileName(title string) string {
	name := exportNameBadChars.ReplaceAllString(title, "_")
	name = strings.Trim(strings.Join(strings.Fields(name), " "), " ._")
	if runes := []rune(name); len(runes) > 80 {
		name = string(runes[:80])
	}
	if name == "" {
		return "document"
	}
	return name
}

// ---------- attachments ----------
