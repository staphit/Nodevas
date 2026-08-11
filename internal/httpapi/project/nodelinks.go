// HTTP handler for node links. Routes: routes.go.

package project

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/project"
)

// maxIndexedNodes bounds the workspace node index: it feeds a picker, and a
// list nobody can scroll is not more useful than a capped one.
const maxIndexedNodes = 5000

// GetNodeIndex lists every node in the workspace, so a link picker can offer
// targets from other projects without a search term. Only ids and titles are
// read, straight from each graph.yaml — no document is opened, and a graph
// that has not changed since the last request is not parsed again.
func (a *API) GetNodeIndex(c *gin.Context) {
	projects, err := a.pm.List()
	if err != nil {
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	type indexedNode struct {
		Project string `json:"project"`
		NodeID  string `json:"nodeId"`
		Title   string `json:"title,omitempty"`
	}
	nodes := make([]indexedNode, 0, 128)
	for _, info := range projects {
		if info.IsFolder || len(nodes) >= maxIndexedNodes {
			continue
		}
		for _, node := range project.NodeTitles(info) {
			if len(nodes) >= maxIndexedNodes {
				break
			}
			nodes = append(nodes, indexedNode{
				Project: info.Name, NodeID: node[0], Title: node[1],
			})
		}
	}
	c.JSON(http.StatusOK, map[string]any{"nodes": nodes})
}

// GetNodeLinks answers "what points here, and where do my links go?" for one
// node. Both halves come from one workspace scan, because the caller needs
// them together and the scan is what costs. The panel asks on every node it
// opens, so the scan reuses its previous result for projects whose files are
// untouched; see project.ScanNodeLinks.
func (a *API) GetNodeLinks(c *gin.Context) {
	targetProject := c.Query("project")
	targetNode := c.Query("node")
	if targetNode == "" {
		httpx.Err(c, http.StatusBadRequest, fmt.Errorf("node is required"))
		return
	}
	if targetProject == "" {
		httpx.Err(c, http.StatusBadRequest, fmt.Errorf("project is required"))
		return
	}
	projects, err := a.pm.List()
	if err != nil {
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	links := project.ScanNodeLinks(projects)
	known := project.NodeIDsByProject(projects)

	backlinks := make([]project.NodeLink, 0, 8)
	outgoing := make([]outgoingLink, 0, 8)
	for _, link := range links {
		if link.ToProject == targetProject && link.ToNode == targetNode {
			backlinks = append(backlinks, link)
		}
		if link.FromProject == targetProject && link.FromNode == targetNode {
			outgoing = append(outgoing, outgoingLink{
				NodeLink: link,
				Missing:  !known[link.ToProject][link.ToNode],
			})
		}
	}
	c.JSON(http.StatusOK, map[string]any{
		"backlinks": backlinks,
		"outgoing":  outgoing,
	})
}

type outgoingLink struct {
	project.NodeLink
	// Missing marks a link whose target is gone: the document still says it,
	// but nothing is there to open.
	Missing bool `json:"missing"`
}
