package node

import (
	"errors"
	"fmt"
	"os"

	"github.com/gin-gonic/gin"
	"nodevas/internal/engine"
	"nodevas/internal/httpapi/httpx"
)

// getDocuments returns revisions without transferring document bodies or UI state.
// Revisions come from current file contents, so external edits are visible too.
func (a *API) getDocuments(c *gin.Context) {
	st := httpx.StoreFor(c.Request, a.pm)
	g, _, err := st.LoadGraph()
	if err != nil {
		httpx.Err(c, 500, err)
		return
	}
	type source struct {
		ID    string `json:"id"`
		Title string `json:"title"`
		Rev   string `json:"rev"`
		Bytes int    `json:"bytes"`
	}
	documents := make([]source, 0, len(g.Nodes))
	for _, n := range g.Nodes {
		if err := c.Request.Context().Err(); err != nil {
			return
		}
		content, rev, err := st.LoadNodeContent(n.ID)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			httpx.Err(c, 500, err)
			return
		}
		documents = append(documents, source{ID: n.ID, Title: n.Title, Rev: rev, Bytes: len(content)})
	}
	c.JSON(200, map[string]any{"documents": documents})
}

// getNodeContext projects one node's metadata, body and prerequisites. Unlike
// getNode, graph membership is required: a deleted node is a 404, not an empty file.
func (a *API) getNodeContext(c *gin.Context) {
	id := c.Param("id")
	if !engine.ValidNodeID(id) {
		httpx.Err(c, 400, errors.New("invalid node id"))
		return
	}
	st := httpx.StoreFor(c.Request, a.pm)
	g, _, err := st.LoadGraph()
	if err != nil {
		httpx.Err(c, 500, err)
		return
	}
	n := g.NodeByID(id)
	if n == nil {
		httpx.Err(c, 404, fmt.Errorf("no node %q in this project", id))
		return
	}
	content, rev, err := st.LoadNodeContent(id)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		httpx.Err(c, 500, err)
		return
	}
	state, err := st.LoadState()
	if err != nil {
		httpx.Err(c, 500, err)
		return
	}
	var upstream, downstream []string
	for _, edge := range g.Edges {
		if edge == nil || !edge.IsPrerequisite() {
			continue
		}
		if edge.To == id {
			upstream = append(upstream, edge.From)
		}
		if edge.From == id {
			downstream = append(downstream, edge.To)
		}
	}
	c.JSON(200, map[string]any{
		"node": n, "content": content, "rev": rev,
		"status":   engine.ComputeStatuses(g, state)[id],
		"upstream": upstream, "downstream": downstream,
	})
}
