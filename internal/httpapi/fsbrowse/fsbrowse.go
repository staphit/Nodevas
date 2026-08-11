// HTTP handlers for the host directory picker.
// Routes: routes.go (routeFilesystem).

package fsbrowse

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"nodevas/internal/httpapi/httpx"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
)

// postFSOpen opens an existing directory in the operating system”s file
// manager. This endpoint is intentionally directory-only: the UI uses it for
// workspace and project roots, never for executing files.
func (a *API) postFSOpen(c *gin.Context) {
	if a.refuseHostFilesystem(c) {
		return
	}
	var body struct {
		Path string `json:"path"`
	}
	if !httpx.DecodeJSON(c, &body) {
		return
	}
	raw := strings.TrimSpace(body.Path)
	if raw == "" {
		httpx.Err(c, http.StatusBadRequest, errors.New("需要資料夾路徑"))
		return
	}
	abs, err := filepath.Abs(filepath.Clean(raw))
	if err != nil {
		httpx.Err(c, http.StatusBadRequest, err)
		return
	}
	info, err := os.Stat(abs)
	if err != nil {
		httpx.Err(c, http.StatusBadRequest, fmt.Errorf("資料夾 %q 不存在或無法存取", abs))
		return
	}
	if !info.IsDir() {
		httpx.Err(c, http.StatusBadRequest, fmt.Errorf("路徑 %q 不是資料夾", abs))
		return
	}
	opener := a.openDir
	if opener == nil {
		opener = OpenDirectory
	}
	if err := opener(abs); err != nil {
		httpx.Err(c, http.StatusUnprocessableEntity, fmt.Errorf("無法開啟檔案管理員：%w", err))
		return
	}
	c.JSON(http.StatusOK, map[string]any{"ok": true, "path": abs})
}

// postFSMkdir creates a new directory inside an existing one, for the folder
// picker's "new folder" button.
func (a *API) postFSMkdir(c *gin.Context) {
	if a.refuseHostFilesystem(c) {
		return
	}
	var body struct {
		Path string `json:"path"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(io.LimitReader(c.Request.Body, httpx.MaxRequestBody)).Decode(&body); err != nil {
		httpx.Err(c, http.StatusBadRequest, err)
		return
	}
	parent := strings.TrimSpace(body.Path)
	name := strings.TrimSpace(body.Name)
	if parent == "" || name == "" {
		httpx.Err(c, http.StatusBadRequest, errors.New("需要父資料夾路徑與新資料夾名稱"))
		return
	}
	if name != filepath.Base(name) || name == "." || name == ".." ||
		strings.ContainsAny(name, `\/:*?"<>|`) {
		httpx.Err(c, http.StatusBadRequest, errors.New("資料夾名稱含有不允許的字元"))
		return
	}
	abs, err := filepath.Abs(filepath.Clean(parent))
	if err != nil {
		httpx.Err(c, http.StatusBadRequest, err)
		return
	}
	info, statErr := os.Stat(abs)
	if statErr != nil {
		if errors.Is(statErr, os.ErrPermission) {
			httpx.Err(c, http.StatusForbidden, fmt.Errorf("無法存取父資料夾 %q：權限不足", abs))
		} else {
			httpx.Err(c, http.StatusBadRequest, fmt.Errorf("父資料夾 %q 不存在", abs))
		}
		return
	}
	if !info.IsDir() {
		httpx.Err(c, http.StatusBadRequest, fmt.Errorf("父路徑 %q 不是資料夾", abs))
		return
	}
	target := filepath.Join(abs, name)
	if _, err := os.Stat(target); err == nil {
		httpx.Err(c, http.StatusConflict, fmt.Errorf("資料夾 %q 已存在", name))
		return
	} else if !errors.Is(err, os.ErrNotExist) {
		status := http.StatusBadRequest
		if errors.Is(err, os.ErrPermission) {
			status = http.StatusForbidden
		}
		httpx.Err(c, status, fmt.Errorf("無法檢查資料夾 %q：%w", target, err))
		return
	}
	if err := os.Mkdir(target, 0o755); err != nil {
		if errors.Is(err, os.ErrExist) {
			httpx.Err(c, http.StatusConflict, fmt.Errorf("資料夾 %q 已存在", name))
			return
		}
		if errors.Is(err, os.ErrPermission) {
			httpx.Err(c, http.StatusForbidden, fmt.Errorf("無法在 %q 建立資料夾：權限不足", abs))
			return
		}
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{"ok": true, "path": target})
}

// getFSDirs powers the folder picker for "import from disk". It lists only
// directories; an empty path returns the drive list on Windows (or / on
// Unix). On a loopback server the caller already has full disk access, so
// this exposes nothing new; on a networked one it is refused.
func (a *API) getFSDirs(c *gin.Context) {
	if a.refuseHostFilesystem(c) {
		return
	}
	raw := strings.TrimSpace(c.Request.URL.Query().Get("path"))
	if raw == "" {
		if runtime.GOOS == "windows" {
			var drives []fsDirEntry
			for letter := 'A'; letter <= 'Z'; letter++ {
				root := string(letter) + `:\`
				if _, err := os.Stat(root); err == nil {
					drives = append(drives, fsDirEntry{Name: string(letter) + ":", Path: root})
				}
			}
			c.JSON(http.StatusOK, map[string]any{
				"path": "", "parent": "", "dirs": drives,
			})
			return
		}
		raw = "/"
	}
	abs, err := filepath.Abs(filepath.Clean(raw))
	if err != nil {
		httpx.Err(c, http.StatusBadRequest, err)
		return
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		httpx.Err(c, http.StatusBadRequest, err)
		return
	}
	dirs := make([]fsDirEntry, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		dirs = append(dirs, fsDirEntry{
			Name: entry.Name(),
			Path: filepath.Join(abs, entry.Name()),
		})
	}
	sort.Slice(dirs, func(i, j int) bool {
		return strings.ToLower(dirs[i].Name) < strings.ToLower(dirs[j].Name)
	})
	parent := filepath.Dir(abs)
	if parent == abs {
		parent = "" // drive root: go back to the drive list
	}
	c.JSON(http.StatusOK, map[string]any{
		"path": abs, "parent": parent, "dirs": dirs,
	})
}

// refuseHostFilesystem blocks the endpoints that hand out the server
// machine's directory tree. On a loopback server the caller already has the
// same disk access; over the network it would be a browsable filesystem for
// anyone with an account, which is not what an account grants.
func (a *API) refuseHostFilesystem(c *gin.Context) bool {
	if a.auth == nil || !a.auth.Remote() {
		return false
	}
	httpx.Err(c, http.StatusForbidden,
		errors.New("browsing the server filesystem is disabled on a networked server"))
	return true
}
