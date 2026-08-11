// The ready queue: which nodes could be worked on right now. Routes: routes.go.

package graph

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"nodevas/internal/engine"
	"nodevas/internal/httpapi/httpx"
)

// Paging bounds. The queue is the one endpoint written for a caller with a
// context window rather than a scrollbar, so it never answers with the whole
// graph unless the whole graph is small.
const (
	defaultReadyLimit = 20
	maxReadyLimit     = 200
)

// getReady answers what is actionable, and — separately — what is waiting and
// on what.
//
// The blocked half is not padding. An agent that asks for work and is handed an
// empty list has to distinguish "the project is finished" from "everything is
// waiting on a person", and those call for opposite next moves.
func (a *API) getReady(c *gin.Context) {
	// Arguments first: a request that cannot be answered should not cost a
	// graph parse and a journal replay before it is turned away.
	limit, err := readLimit(c.Query("limit"))
	if err != nil {
		httpx.Err(c, http.StatusBadRequest, err)
		return
	}
	st := httpx.StoreFor(c.Request, a.pm)
	g, _, err := st.LoadGraph()
	if err != nil {
		httpx.Err(c, 500, err)
		return
	}
	rs, err := st.LoadState()
	if err != nil {
		httpx.Err(c, 500, err)
		return
	}
	readiness := engine.ComputeReadiness(g, rs)
	ready := filterTasks(readiness.Ready, c.Query("assignee"), c.Query("tag"))
	blocked := filterTasks(readiness.Blocked, c.Query("assignee"), c.Query("tag"))

	page, next := paginate(ready, c.Query("cursor"), limit)
	body := map[string]any{
		"tasks":   page,
		"ready":   len(ready),
		"busy":    readiness.Busy,
		"waiting": len(blocked),
	}
	if next != "" {
		body["cursor"] = next
	}
	// The blocked listing is opt-in: on a large board it is longer than the
	// queue itself, and the common call is "what can I do", not "why not".
	if boolQuery(c.Query("includeBlocked")) {
		blockedPage, _ := paginate(blocked, "", limit)
		body["blocked"] = blockedPage
	}
	c.JSON(200, body)
}

// readLimit parses ?limit=, refusing nonsense rather than clamping it silently:
// a caller who asked for 5000 should learn the ceiling exists.
func readLimit(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultReadyLimit, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 {
		return 0, errBadLimit
	}
	if limit > maxReadyLimit {
		return 0, errBadLimit
	}
	return limit, nil
}

var errBadLimit = &limitError{}

type limitError struct{}

func (*limitError) Error() string {
	return "limit must be a whole number between 1 and " + strconv.Itoa(maxReadyLimit)
}

func boolQuery(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes":
		return true
	}
	return false
}

// filterTasks narrows the queue to one person's work or one tag.
func filterTasks(tasks []engine.ReadyNode, assignee, tag string) []engine.ReadyNode {
	assignee = strings.TrimSpace(assignee)
	tag = strings.TrimSpace(tag)
	if assignee == "" && tag == "" {
		return tasks
	}
	kept := make([]engine.ReadyNode, 0, len(tasks))
	for _, task := range tasks {
		if assignee != "" && !strings.EqualFold(task.Assignee, assignee) {
			continue
		}
		if tag != "" && !hasTag(task.Tags, tag) {
			continue
		}
		kept = append(kept, task)
	}
	return kept
}

func hasTag(tags []string, want string) bool {
	for _, tag := range tags {
		if strings.EqualFold(tag, want) {
			return true
		}
	}
	return false
}

// paginate walks the queue from the node after the cursor.
//
// The cursor is a node id rather than an offset. The queue is recomputed on
// every call and its contents shift as work finishes, so an offset would skip
// or repeat entries; an id resumes from the same place in the new order. An id
// that is no longer in the queue — the usual case, since the caller most likely
// just finished it — starts from the top, which is where the next work is.
func paginate(tasks []engine.ReadyNode, cursor string, limit int) ([]engine.ReadyNode, string) {
	start := 0
	if cursor = strings.TrimSpace(cursor); cursor != "" {
		for index, task := range tasks {
			if task.ID == cursor {
				start = index + 1
				break
			}
		}
	}
	if start >= len(tasks) {
		return []engine.ReadyNode{}, ""
	}
	end := start + limit
	if end >= len(tasks) {
		return tasks[start:], ""
	}
	page := tasks[start:end]
	return page, page[len(page)-1].ID
}
