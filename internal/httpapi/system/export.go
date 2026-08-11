// HTTP handlers for exporting a node or subtree to a file.
// Routes: routes.go (routeTransfer).

package system

import (
	"errors"
	"fmt"
	"mime"
	"net/http"
	"nodevas/internal/export"
	"nodevas/internal/httpapi/httpx"
	"os"
	"strings"

	"github.com/gin-gonic/gin"

	"nodevas/internal/document"
	"nodevas/internal/document/docx"
	dochtml "nodevas/internal/document/html"
)

func (a *API) postExport(c *gin.Context) {
	var request export.Request
	if !httpx.DecodeJSON(c, &request) {
		return
	}
	spec, ok := export.Formats[strings.ToLower(strings.TrimSpace(request.Format))]
	if !ok {
		httpx.Err(c, http.StatusBadRequest, fmt.Errorf("unsupported format %q", request.Format))
		return
	}
	st := httpx.StoreFor(c.Request, a.pm)
	markdown, title, err := export.BuildMarkdown(st, request)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, os.ErrNotExist) {
			status = http.StatusNotFound
		}
		httpx.Err(c, status, err)
		return
	}
	options := document.Options{Title: title, Assets: st.DocumentAssets()}
	var data []byte
	switch strings.ToLower(request.Format) {
	case "md":
		data = []byte(markdown)
	case "txt":
		data = []byte(document.RenderText(document.Parse(markdown)))
	case "html":
		data = []byte(dochtml.RenderHTML(document.Parse(markdown), options))
	case "docx":
		data, err = docx.RenderDOCX(document.Parse(markdown), options)
		if err != nil {
			httpx.Err(c, http.StatusInternalServerError, err)
			return
		}
	}
	name := export.FileName(title) + spec.Extension
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": name})
	if disposition == "" {
		disposition = "attachment"
	}
	c.Header("Content-Type", spec.Mime)
	c.Header("Content-Disposition", disposition)
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Length", fmt.Sprint(len(data)))
	if _, err := c.Writer.Write(data); err != nil {
		return
	}
}
