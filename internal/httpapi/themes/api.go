// Package themes serves theme packs to the people who may pick them:
//
//	GET    /api/themes                  → {packs, default}
//	POST   /api/themes                  multipart "file" → 201 and the pack
//	GET    /api/themes/:id              → the stored theme.json
//	GET    /api/themes/:id/files/*path  → an image's bytes
//	DELETE /api/themes/:id              → 204 (the owner or an administrator)
//	PUT    /api/themes/default {id}     → {default} (administrators)
//
// Every one of them is a person's: an agent is refused, reads included.
package themes

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"nodevas/internal/audit"
	"nodevas/internal/auth"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/locale"
	"nodevas/internal/themes"
)

const (
	// multipartEnvelope is room for the parts around a zip at its limit.
	multipartEnvelope = 1 << 20
	// multipartMemory is how much of an upload stays in memory; the rest
	// waits in a temporary file, so a large zip does not cost its size in RAM.
	multipartMemory = 1 << 20
	// uploadWindow replaces the minute the server gives a request's body: the
	// largest zip takes longer than that on an ordinary line. Only a signed-in
	// person's upload gets here.
	uploadWindow = 5 * time.Minute
	// responsePolicy keeps a pack's file from doing anything but be a picture,
	// should it ever be opened as a page.
	responsePolicy = "sandbox; default-src 'none'"
	// imageCache is the longest a replaced pack's image may keep showing.
	imageCache = "private, max-age=3600"

	maxDefaultBody = 4 << 10
)

// API exposes a themes.Store over HTTP.
type API struct {
	store   *themes.Store
	changed func(id string)
}

// New builds the handlers; changed hears each change, with the pack's id.
func New(store *themes.Store, changed func(id string)) *API {
	return &API{store: store, changed: changed}
}

// Register wires `/api/themes*` onto the router group.
func (a *API) Register(api *gin.RouterGroup) {
	group := api.Group("/themes")
	group.Use(requirePerson)
	group.GET("", a.getThemes)
	group.POST("", a.postTheme)
	group.GET("/:id", a.getManifest)
	group.GET("/:id/files/*path", a.getFile)
	group.DELETE("/:id", a.deleteTheme)
	group.PUT("/default", httpx.RequireAdmin, a.putDefault)
}

func requirePerson(c *gin.Context) {
	actor, attached := auth.AttachedActor(c.Request)
	if !attached || actor.ID == "" {
		httpx.Err(c, http.StatusUnauthorized, locale.Errorf("sign in to use theme packs", "請先登入才能使用主題套件"))
		return
	}
	if actor.IsAgent() {
		httpx.Err(c, http.StatusForbidden, locale.Errorf("theme packs are for people, not agents", "主題套件只供人使用，agent 不能使用"))
		return
	}
	c.Next()
}

func (a *API) getThemes(c *gin.Context) {
	ctx := c.Request.Context()
	packs, err := a.store.List(ctx)
	if err != nil {
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	current, err := a.store.Default(ctx)
	if err != nil {
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	actor := auth.ActorFrom(c.Request)
	for i := range packs {
		packs[i] = packs[i].For(actor)
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, map[string]any{"packs": packs, "default": current})
}

func (a *API) postTheme(c *gin.Context) {
	limits := a.store.Limits()
	if c.Request.ContentLength > limits.MaxZipBytes+multipartEnvelope {
		httpx.ErrWithStatus(c, limits.TooLarge())
		return
	}
	// Not every server has these to extend (a test's recorder does not).
	controller := http.NewResponseController(c.Writer)
	_ = controller.SetReadDeadline(time.Now().Add(uploadWindow))
	_ = controller.SetWriteDeadline(time.Now().Add(2 * uploadWindow))

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limits.MaxZipBytes+multipartEnvelope)
	if err := c.Request.ParseMultipartForm(multipartMemory); err != nil {
		var over *http.MaxBytesError
		if errors.As(err, &over) {
			httpx.ErrWithStatus(c, limits.TooLarge())
			return
		}
		httpx.Err(c, http.StatusBadRequest, locale.Errorf("send the zip as a multipart field named file", "請以名為 file 的 multipart 欄位傳送 zip"))
		return
	}
	defer c.Request.MultipartForm.RemoveAll()
	upload, header, err := c.Request.FormFile("file")
	if err != nil {
		httpx.Err(c, http.StatusBadRequest, locale.Errorf("file is required", "需要提供檔案"))
		return
	}
	defer upload.Close()

	actor := auth.ActorFrom(c.Request)
	ctx := c.Request.Context()
	pack, err := a.store.Import(ctx, actor.ID, upload, header.Size)
	if err != nil {
		httpx.ErrWithStatus(c, err)
		return
	}
	audit.Annotate(ctx, audit.Annotation{Target: pack.ID, Detail: map[string]any{"version": pack.Version, "sizeBytes": pack.SizeBytes}})
	a.changed(pack.ID)
	c.JSON(http.StatusCreated, pack.For(actor))
}

func (a *API) getManifest(c *gin.Context) {
	a.serve(c, c.Param("id"), themes.ManifestFile, "application/json", httpx.CacheControlRevalidate)
}

func (a *API) getFile(c *gin.Context) {
	name := strings.TrimPrefix(c.Param("path"), "/")
	contentType, ok := themes.ContentType(name)
	if !ok {
		notFound(c)
		return
	}
	a.serve(c, c.Param("id"), name, contentType, imageCache)
}

// serve answers with one stored file. The type is declared here, so nothing
// sniffs it, and the policy is per response: the site's own lets a page run.
func (a *API) serve(c *gin.Context, id, name, contentType, cacheControl string) {
	file, info, err := a.store.Open(c.Request.Context(), id, name)
	if err != nil {
		if errors.Is(err, themes.ErrNotFound) {
			notFound(c)
			return
		}
		c.Header("Cache-Control", "no-store")
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	defer file.Close()
	c.Header("Content-Type", contentType)
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Security-Policy", responsePolicy)
	// Size and time of writing: extraction writes every file anew, so a
	// replaced pack is a new tag even where a file's bytes did not change.
	httpx.FileETag(c, info.Size(), info.ModTime().UnixNano())
	c.Header("Cache-Control", cacheControl)
	http.ServeContent(c.Writer, c.Request, "", info.ModTime(), file)
}

func notFound(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	httpx.ErrWithStatus(c, themes.ErrNotFound)
}

func (a *API) deleteTheme(c *gin.Context) {
	id := c.Param("id")
	ctx := c.Request.Context()
	if err := a.store.Delete(ctx, auth.ActorFrom(c.Request), id); err != nil {
		httpx.ErrWithStatus(c, err)
		return
	}
	audit.Annotate(ctx, audit.Annotation{Target: id})
	a.changed(id)
	c.Status(http.StatusNoContent)
	c.Writer.WriteHeaderNow()
}

func (a *API) putDefault(c *gin.Context) {
	var body struct {
		ID string `json:"id"`
	}
	if !httpx.DecodeJSONLimit(c, &body, maxDefaultBody) {
		return
	}
	ctx := c.Request.Context()
	if err := a.store.SetDefault(ctx, body.ID); err != nil {
		httpx.ErrWithStatus(c, err)
		return
	}
	audit.Annotate(ctx, audit.Annotation{Target: body.ID})
	a.changed(body.ID)
	c.JSON(http.StatusOK, map[string]string{"default": body.ID})
}
