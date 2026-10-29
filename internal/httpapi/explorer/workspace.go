package explorer

import (
	"errors"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"

	"nodevas/internal/gitrepo"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/locale"
	"nodevas/internal/store"
)

func (a *API) workspaceFiles(c *gin.Context) {
	if _, ok := person(c); !ok {
		return
	}
	st, _, ok := a.project(c)
	if !ok {
		return
	}
	out, err := st.WorkspaceFiles()
	if err != nil {
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, out)
}

func (a *API) workspaceFile(c *gin.Context) {
	if _, ok := person(c); !ok {
		return
	}
	st, _, ok := a.project(c)
	if !ok {
		return
	}
	out, err := st.ReadWorkspaceFile(c.Query("path"))
	switch {
	case errors.Is(err, store.ErrHiddenFile):
		httpx.Coded(c, http.StatusForbidden, "HIDDEN_FILE", err, nil)
		return
	case errors.Is(err, store.ErrBadPath):
		httpx.Coded(c, http.StatusBadRequest, "BAD_PATH", locale.Errorf("the path is not a file inside the project", "這個路徑不是專案內的檔案"), nil)
		return
	case errors.Is(err, os.ErrNotExist):
		httpx.Coded(c, http.StatusNotFound, "NOT_FOUND", locale.Errorf("no such file", "沒有這個檔案"), nil)
		return
	case err != nil:
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, out)
}

func (a *API) workspaceGit(c *gin.Context) {
	if _, ok := person(c); !ok {
		return
	}
	st, _, ok := a.project(c)
	if !ok {
		return
	}
	out, err := st.HistoryGit(c.Request.Context())
	if err != nil {
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, out)
}

func (a *API) workspaceCommit(c *gin.Context) {
	if _, ok := person(c); !ok {
		return
	}
	st, _, ok := a.project(c)
	if !ok {
		return
	}
	sha := strings.TrimSpace(c.Query("sha"))
	if !gitrepo.ValidSHA(sha) {
		httpx.Coded(c, http.StatusBadRequest, "BAD_PATH", locale.Errorf("sha is a full commit id", "sha 須為完整的提交代號"), nil)
		return
	}
	out, err := st.HistoryCommitPatch(c.Request.Context(), sha)
	switch {
	case errors.Is(err, store.ErrNoGitHistory):
		httpx.Coded(c, http.StatusConflict, "FAILED_PRECONDITION", err, nil)
		return
	case errors.Is(err, store.ErrCommitNotFound):
		httpx.Coded(c, http.StatusNotFound, "NOT_FOUND", err, nil)
		return
	case errors.Is(err, store.ErrBadPath):
		httpx.Coded(c, http.StatusBadRequest, "BAD_PATH", err, nil)
		return
	case err != nil:
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, out)
}
