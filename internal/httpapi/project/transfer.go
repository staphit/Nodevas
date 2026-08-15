// HTTP handlers for project import and export.
// Routes: routes.go (routeTransfer).

package project

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/project"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	maxConcurrentProjectExports = 2
	projectExportIdleTimeout    = 30 * time.Second
)

var projectExportSlots = make(chan struct{}, maxConcurrentProjectExports)

func acquireProjectExport(ctx context.Context) bool {
	if ctx.Err() != nil {
		return false
	}
	select {
	case projectExportSlots <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	default:
		return false
	}
}

func releaseProjectExport() { <-projectExportSlots }

type contextWriter struct {
	ctx context.Context
	w   io.Writer
}

type exportResponseWriter struct {
	http.ResponseWriter
	controller *http.ResponseController
	ctx        context.Context
}

func (w exportResponseWriter) Write(data []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	_ = w.controller.SetWriteDeadline(time.Now().Add(projectExportIdleTimeout))
	return w.ResponseWriter.Write(data)
}

func (w contextWriter) Write(data []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	return w.w.Write(data)
}

// GetProjectExport downloads a project as a ZIP-compatible .veproj. Without a
// ?project= parameter it exports the active project. A folder, or a project
// that has sub-projects, is exported as a bundle holding the whole subtree.
func (a *API) GetProjectExport(c *gin.Context) {
	if !acquireProjectExport(c.Request.Context()) {
		if c.Request.Context().Err() == nil {
			c.Header("Retry-After", "2")
			httpx.Err(c, http.StatusTooManyRequests, fmt.Errorf("too many project exports in progress"))
		}
		return
	}
	defer releaseProjectExport()

	scope, err := a.pm.ExportScope(strings.TrimSpace(c.Request.URL.Query().Get("project")))
	if err != nil {
		httpx.Err(c, http.StatusBadRequest, err)
		return
	}
	root := scope.Root()
	file, err := os.CreateTemp("", "nodevas-*.veproj")
	if err != nil {
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	name := file.Name()
	defer os.Remove(name)
	buildErr := error(nil)
	writer := contextWriter{ctx: c.Request.Context(), w: file}
	if scope.IsBundle() {
		buildErr = project.BuildProjectBundle(writer, scope)
	} else {
		buildErr = project.BuildProjectArchive(writer, root, scope.SharedStatuses())
	}
	if buildErr != nil {
		_ = file.Close()
		if c.Request.Context().Err() == nil {
			httpx.Err(c, http.StatusInternalServerError, buildErr)
		}
		return
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		_ = file.Close()
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	defer file.Close()

	downloadName := root.Name
	if downloadName == "." || strings.Contains(downloadName, "/") {
		downloadName = filepath.Base(root.Path)
	}
	disposition := mime.FormatMediaType("attachment", map[string]string{
		"filename": downloadName + ".veproj",
	})
	c.Header("Content-Type", "application/vnd.nodevas.project+zip")
	c.Header("Content-Disposition", disposition)
	c.Header("X-Content-Type-Options", "nosniff")
	controller := http.NewResponseController(c.Writer)
	response := http.ResponseWriter(c.Writer)
	if err := controller.SetWriteDeadline(time.Now().Add(projectExportIdleTimeout)); err == nil {
		defer controller.SetWriteDeadline(time.Time{})
		response = exportResponseWriter{
			ResponseWriter: c.Writer,
			controller:     controller,
			ctx:            c.Request.Context(),
		}
	}
	http.ServeContent(response, c.Request, downloadName+".veproj", info.ModTime(), file)
}

// PostProjectImport imports a .veproj or ZIP archive and activates a project
// from it. Existing projects are never overwritten. The optional "mode" field
// chooses between the wrapper directory and unpacking a bundle into the
// workspace itself, and the optional "parent" field imports into an existing
// project or folder instead of the workspace root.
func (a *API) PostProjectImport(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, project.MaxProjectArchiveBytes)
	if err := c.Request.ParseMultipartForm(project.MaxProjectArchiveBytes); err != nil {
		httpx.Err(c, http.StatusBadRequest, fmt.Errorf("read project archive: %w", err))
		return
	}
	if c.Request.MultipartForm != nil {
		defer c.Request.MultipartForm.RemoveAll()
	}
	upload, header, err := c.Request.FormFile("file")
	if err != nil {
		httpx.Err(c, http.StatusBadRequest, fmt.Errorf("file is required"))
		return
	}
	defer upload.Close()
	data, err := io.ReadAll(io.LimitReader(upload, project.MaxProjectArchiveBytes+1))
	if err != nil {
		httpx.Err(c, http.StatusBadRequest, err)
		return
	}
	// The optional "mode" field decides whether a workspace bundle unpacks into
	// one wrapper project or straight into the workspace. A client that does
	// not send it, or sends anything else, gets the wrapper it has always got.
	// The optional "parent" field names an existing project or folder to import
	// under; absent or empty means the workspace root, so a client that knows
	// nothing about it imports exactly where it always did.
	activateName, destination, err := a.pm.InstallArchiveMode(
		data,
		c.Request.FormValue("name"),
		header.Filename,
		project.ParseImportMode(c.Request.FormValue("mode")),
		c.Request.FormValue("parent"),
	)
	if err != nil {
		httpx.ErrWithStatus(c, err)
		return
	}
	c.JSON(http.StatusCreated, map[string]string{
		"active": activateName,
		"root":   a.visibleHostPath(destination),
	})
}
