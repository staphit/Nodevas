// The id/label/link rewriting engine a transfer runs its copied content
// through: dependency expressions, wire-vertex keys, attachment URLs, and
// `[[node link]]` targets in the moved documents.

package store

import (
	"bytes"
	"path/filepath"
	"regexp"
	"strings"

	"nodevas/internal/engine"
	"nodevas/internal/engine/dsl"
)

// transferNodeLabel is what a node is called in a message.
func transferNodeLabel(node *engine.Node) string {
	if title := strings.TrimSpace(node.Title); title != "" {
		return title
	}
	return node.ID
}

// rewriteRequires retargets a dependency expression at the copied nodes.
//
// A reference to a node that stayed behind cannot be kept — it would dangle —
// and cannot be partially removed either without silently changing what the
// expression means. So such an expression is dropped whole and reported;
// ok=false says that happened.
func rewriteRequires(requires string, selection map[string]bool, rename func(string) string) (string, bool) {
	if strings.TrimSpace(requires) == "" {
		return "", true
	}
	expr, parseErr := dsl.Parse(requires)
	if parseErr != nil || expr == nil {
		return "", parseErr == nil
	}
	for _, ref := range dsl.NodeRefs(expr) {
		if !selection[ref] {
			return "", false
		}
	}
	dsl.RenameNodeRefs(expr, rename)
	return expr.String(), true
}

func renameWireKey(key string, rename func(string) string) string {
	if target, ok := strings.CutPrefix(key, "gate:"); ok {
		return "gate:" + rename(target)
	}
	from, to, _ := strings.Cut(key, "->")
	return rename(from) + "->" + rename(to)
}

// rewriteAttachmentLinks retargets /api/nodes/<id>/files/ URLs at the copy,
// so a moved document keeps showing its own images instead of the original's.
func rewriteAttachmentLinks(content []byte, oldID, newID string) []byte {
	if len(content) == 0 || oldID == newID {
		return content
	}
	return []byte(strings.ReplaceAll(
		string(content),
		"/api/nodes/"+oldID+"/files/",
		"/api/nodes/"+newID+"/files/",
	))
}

// rewritePageAttachmentLinks does the same for subpages, skipping the binary
// formats where a byte-level replacement would corrupt the file.
func rewritePageAttachmentLinks(name string, data []byte, oldID, newID string) []byte {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".md", ".txt", ".html":
		return rewriteAttachmentLinks(data, oldID, newID)
	}
	return data
}

// nodeLinkPattern matches one `[[target]]` or `[[target|label]]`. It is a copy
// of the parser in internal/project (nodelinks.go), which owns the syntax but
// imports this package and so cannot be imported back. Neither the target nor
// the label may span a newline, so prose that merely looks like a link across
// two lines is never touched.
var nodeLinkPattern = regexp.MustCompile(`\[\[([^\]|\n]+?)(?:\|([^\]\n]*))?\]\]`)

// splitNodeLinkTarget splits "Story/node-0012" into project and node at the
// last slash, because project names are nested paths. An empty project means
// the document's own project. Mirrors project.SplitLinkTarget.
func splitNodeLinkTarget(target string) (project, nodeID string) {
	trimmed := strings.Trim(strings.TrimSpace(target), "/")
	if slash := strings.LastIndex(trimmed, "/"); slash >= 0 {
		return strings.TrimSpace(trimmed[:slash]), strings.TrimSpace(trimmed[slash+1:])
	}
	return "", trimmed
}

// rewriteNodeLinks keeps a moved document's node links pointing where they
// pointed before the move.
//
// A bare `[[node-1]]` means "node-1 in my own project", and the document's own
// project is exactly what a transfer changes: left alone, the link would
// quietly resolve to whatever carries that id in the target project. So a
// target that travels with the selection stays bare and follows its new id,
// and one that stays behind is qualified with the project it stayed in. A
// target that already names a project is unaffected by the move.
//
// Links inside fenced code are rewritten too. That is deliberate: the reader
// side (project.ParseNodeLinks, and the backlinks built from it) does not
// exempt fences either, so such a link is live everywhere else in the product,
// and skipping it here is the one thing that would leave it pointing at the
// wrong node.
func rewriteNodeLinks(content []byte, sourceProject string, ids map[string]string) []byte {
	if !bytes.Contains(content, []byte("[[")) {
		return content
	}
	return []byte(nodeLinkPattern.ReplaceAllStringFunc(string(content), func(match string) string {
		raw := nodeLinkPattern.FindStringSubmatch(match)[1]
		project, nodeID := splitNodeLinkTarget(raw)
		if project != "" || nodeID == "" {
			return match
		}
		target := ""
		switch newID, moved := ids[nodeID]; {
		case moved:
			target = newID
		case sourceProject != "":
			target = sourceProject + "/" + nodeID
		default:
			return match
		}
		// Only the target is replaced, so the label survives byte for byte.
		return "[[" + target + match[len("[[")+len(raw):]
	}))
}
