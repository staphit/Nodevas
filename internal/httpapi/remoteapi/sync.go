package remoteapi

import (
	"errors"
	"net/http"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/remote"

	"github.com/gin-gonic/gin"
)

// getRemoteSyncStatus compares the current local workspace snapshot with the
// last successful remote snapshot. It is read-only and safe to call at startup.
func (a *API) getRemoteSyncStatus(c *gin.Context) {
	if a.remotes.Load().Kind == "drive" {
		if !a.ensureDriveOwner(c) {
			return
		}
	}
	status, err := a.remotes.SyncStatus(c.Request.Context())
	if err != nil {
		httpx.Err(c, http.StatusBadGateway, err)
		return
	}
	c.JSON(http.StatusOK, status)
}

// postRemoteSyncFlush explicitly uploads the current local snapshot when the
// sync status says it is safe. Conflict decisions stay explicit in the UI.
func (a *API) postRemoteSyncFlush(c *gin.Context) {
	if a.remotes.Load().Kind == "drive" {
		if !a.ensureDriveOwner(c) {
			return
		}
	}
	ref, err := a.remotes.FlushBackup(c.Request.Context())
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, remote.ErrSyncConflict) {
			status = http.StatusConflict
		} else if errors.Is(err, remote.ErrNoRemote) {
			status = http.StatusBadRequest
		}
		httpx.Err(c, status, err)
		return
	}
	status, err := a.remotes.SyncStatus(c.Request.Context())
	if err != nil {
		httpx.Err(c, http.StatusBadGateway, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"state":  status.State,
		"bundle": ref,
	})
}
