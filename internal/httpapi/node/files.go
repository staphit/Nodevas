// HTTP handlers for a node's file attachments.
// Routes: routes.go (routePages).

package node

import (
	"mime"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"nodevas/internal/engine"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/locale"
	"nodevas/internal/store"
)

func (a *API) postNodeFile(c *gin.Context) {
	id := c.Param("id")
	if !engine.ValidNodeID(id) {
		httpx.Err(c, http.StatusBadRequest, locale.Errorf("invalid node id", "節點代號無效"))
		return
	}
	st := httpx.StoreFor(c.Request, a.pm)
	if info, err := store.StatProjectPath(st.Root(), st.NodePath(id)); err != nil || !info.Mode().IsRegular() {
		httpx.Err(c, http.StatusNotFound, locale.Errorf("node %q not found", "找不到節點「%s」", id))
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, store.MaxAttachmentBytes)
	if err := c.Request.ParseMultipartForm(32 << 20); err != nil {
		httpx.Err(c, http.StatusBadRequest, locale.Errorf("read upload: %w", "讀取上傳檔案：%w", err))
		return
	}
	if c.Request.MultipartForm != nil {
		defer c.Request.MultipartForm.RemoveAll()
	}
	upload, header, err := c.Request.FormFile("file")
	if err != nil {
		httpx.Err(c, http.StatusBadRequest, locale.Errorf("file is required", "需要提供檔案"))
		return
	}
	defer upload.Close()

	name, err := st.SaveAttachment(id, header.Filename, upload)
	if err != nil {
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{
		"ok":   true,
		"name": name,
		"url":  store.AttachmentURL(id, name),
	})
}

func (a *API) getNodeFile(c *gin.Context) {
	id := c.Param("id")
	file := c.Param("file")
	if !engine.ValidNodeID(id) {
		httpx.Err(c, http.StatusBadRequest, locale.Errorf("invalid node id", "節點代號無效"))
		return
	}
	if file == "" || file != filepath.Base(file) || strings.Contains(file, "..") {
		httpx.Err(c, http.StatusBadRequest, locale.Errorf("invalid file name", "檔名無效"))
		return
	}
	st := httpx.StoreFor(c.Request, a.pm)
	dir := st.NodeFilesDir(id)
	path := filepath.Join(dir, file)
	attachment, info, err := store.OpenProjectFile(st.Root(), path)
	if err != nil {
		httpx.Err(c, http.StatusNotFound, locale.Errorf("attachment not found", "找不到附件"))
		return
	}
	defer attachment.Close()
	// Declare the type ourselves. Left unset, http.ServeFile sniffs the body,
	// so an attachment with an unknown extension holding "<!DOCTYPE html" is
	// served as text/html from this origin and executes as the viewer.
	kind := "attachment"
	contentType := "application/octet-stream"
	if inline, ok := store.InlineContentType(file); ok {
		kind = "inline"
		contentType = inline
	}
	disposition := mime.FormatMediaType(kind, map[string]string{"filename": file})
	if disposition == "" {
		disposition = kind
	}
	c.Header("Content-Type", contentType)
	c.Header("Content-Disposition", disposition)
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Security-Policy", "sandbox")
	// An ETag, never a lifetime. UniqueAttachmentPath keeps the name stable for
	// a given upload, but a later upload can land on that same name, so the URL
	// is not immutable. ServeContent answers the If-None-Match itself once the
	// tag is set, which is why this is a header rather than the ETag wrapper:
	// hashing the body would mean reading the whole attachment into memory.
	httpx.FileETag(c, info.Size(), info.ModTime().UnixNano())
	http.ServeContent(c.Writer, c.Request, file, info.ModTime(), attachment)
}
