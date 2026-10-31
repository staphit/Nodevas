// HTTP handler for opening one of the bundled example projects.
// Routes: routes.go.

package project

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"nodevas/demo"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/locale"
	"nodevas/internal/project"
)

// PostProjectExample copies a bundled example (repository demo/, embedded in
// the binary) into the workspace as a new project and opens it. Body:
// {example, language}: the copy in that interface language when the
// example has one, else the English original. The new project's name is the example's, or the first free
// "<example>-2", "-3"… — opening the same example twice gives two copies.
func (a *API) PostProjectExample(c *gin.Context) {
	var body struct {
		Example  string `json:"example"`
		Language string `json:"language"`
	}
	if !httpx.DecodeJSON(c, &body) {
		return
	}
	example := strings.TrimSpace(body.Example)
	if example == "" {
		httpx.Err(c, http.StatusBadRequest, locale.Errorf("body must be {example}", "內容須為 {example}"))
		return
	}
	name, err := a.pm.InstallExample(demo.ExamplesFor(body.Language), example)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, project.ErrUnknownExample) {
			status = http.StatusNotFound
		}
		httpx.Err(c, status, err)
		return
	}
	a.follow(c.Request, name)
	a.pm.Hub().Broadcast("project-changed", name)
	c.JSON(http.StatusOK, map[string]any{"ok": true, "name": name, "active": name})
}
