package remoteapi

import (
	"fmt"
	"net/http"
	"nodevas/internal/auth"
	"nodevas/internal/httpapi/httpx"

	"github.com/gin-gonic/gin"
)

// getDriveCredentials returns status only. Client ID and secret never leave
// the server after they are submitted.
func (a *API) getDriveCredentials(c *gin.Context) {
	configured, source := a.remotes.DriveCredentialStatus()
	c.JSON(http.StatusOK, gin.H{
		"configured": configured,
		"source":     source,
	})
}

// putDriveCredentials stores credentials in the encrypted app secrets store,
// never in the project workspace or its remote backup.
func (a *API) putDriveCredentials(c *gin.Context) {
	if a.remotes.DriveOwnedByAnother(auth.ActorFrom(c.Request).ID) {
		httpx.Err(c, http.StatusForbidden, fmt.Errorf("Google Drive is connected by another administrator"))
		return
	}
	if a.pm != nil && a.pm.IsRemote() && !auth.RequestIsSecure(c.Request) {
		httpx.Err(c, http.StatusUpgradeRequired, fmt.Errorf("設定 OAuth 憑證需要 HTTPS"))
		return
	}
	var body struct {
		ClientID     string `json:"clientId"`
		ClientSecret string `json:"clientSecret"`
	}
	if !httpx.DecodeJSON(c, &body) {
		return
	}
	if err := a.remotes.SaveDriveCredentials(body.ClientID, body.ClientSecret); err != nil {
		httpx.Err(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"configured": true,
		"source":     "app",
	})
}

// deleteDriveCredentials removes the app-entered credential.
func (a *API) deleteDriveCredentials(c *gin.Context) {
	if a.remotes.DriveOwnedByAnother(auth.ActorFrom(c.Request).ID) {
		httpx.Err(c, http.StatusForbidden, fmt.Errorf("Google Drive is connected by another administrator"))
		return
	}
	if err := a.remotes.ClearDriveCredentials(); err != nil {
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	configured, source := a.remotes.DriveCredentialStatus()
	c.JSON(http.StatusOK, gin.H{
		"configured": configured,
		"source":     source,
	})
}
