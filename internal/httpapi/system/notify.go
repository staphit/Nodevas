// HTTP handlers for notification settings.
// Routes: routes.go (routeSystem).

package system

import (
	"errors"
	"net/http"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/identity"
	"nodevas/internal/notify"
	"strings"

	"github.com/gin-gonic/gin"
)

const (
	maxNotifySettingsBody = 64 << 10
	maxNotifyTestBody     = 8 << 10
)

func (a *API) authorizeNotifyManagement(c *gin.Context) (identity.Actor, bool) {
	if a.auth == nil || !a.auth.Remote() {
		return identity.Local, true
	}
	actor, err := a.auth.Authenticate(c.Request)
	if err != nil {
		httpx.Err(c, http.StatusUnauthorized, err)
		return identity.Actor{}, false
	}
	if !actor.IsAdmin() {
		httpx.Err(c, http.StatusForbidden, errors.New("notification administration requires an owner or administrator"))
		return identity.Actor{}, false
	}
	return actor, true
}

// getNotifySettings never returns the stored password: the UI only needs to
// know whether one is set.
func (a *API) getNotifySettings(c *gin.Context) {
	if _, ok := a.authorizeNotifyManagement(c); !ok {
		return
	}
	settings := a.notifier.Settings()
	hasPassword := settings.SMTPPass != ""
	settings.SMTPPass = ""
	c.JSON(http.StatusOK, map[string]any{
		"settings":    settings,
		"hasPassword": hasPassword,
	})
}

func (a *API) putNotifySettings(c *gin.Context) {
	if _, ok := a.authorizeNotifyManagement(c); !ok {
		return
	}
	var settings notify.NotifySettings
	if !httpx.DecodeJSONLimit(c, &settings, maxNotifySettingsBody) {
		return
	}
	if settings.LeadMinutes <= 0 || settings.LeadMinutes > 60*24*60 {
		httpx.Err(c, http.StatusBadRequest, errors.New("提前時間需介於 1 分鐘到 60 天之間"))
		return
	}
	if settings.SMTPPort <= 0 || settings.SMTPPort > 65535 {
		httpx.Err(c, http.StatusBadRequest, errors.New("SMTP 連接埠無效"))
		return
	}
	if settings.Enabled && (settings.SMTPHost == "" || settings.From == "") {
		httpx.Err(c, http.StatusBadRequest, errors.New("啟用通知需要 SMTP 主機與寄件人"))
		return
	}
	err := a.notifier.SaveSettings(settings)
	if err != nil {
		if errors.Is(err, notify.ErrSMTPPasswordRequired) {
			httpx.Err(c, http.StatusBadRequest, err)
			return
		}
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{"ok": true})
}

func (a *API) postNotifyTest(c *gin.Context) {
	actor, ok := a.authorizeNotifyManagement(c)
	if !ok {
		return
	}
	var payload struct {
		To string `json:"to"`
	}
	if !httpx.DecodeJSONLimit(c, &payload, maxNotifyTestBody) {
		return
	}
	settings := a.notifier.Settings()
	to := strings.TrimSpace(payload.To)
	if to == "" {
		to = strings.TrimSpace(settings.DefaultTo)
	}
	if to == "" {
		httpx.Err(c, http.StatusBadRequest, errors.New("缺少收件人"))
		return
	}
	if settings.SMTPHost == "" || settings.From == "" {
		httpx.Err(c, http.StatusBadRequest, errors.New("請先設定 SMTP 主機與寄件人"))
		return
	}
	if err := a.notifier.SendTestMail(actor.ID, to,
		"[Nodevas] 測試信",
		"SMTP 設定正確,截止提醒將寄到此信箱。\r\n"); err != nil {
		if errors.Is(err, notify.ErrMailRateLimited) || errors.Is(err, notify.ErrMailBusy) {
			httpx.Err(c, http.StatusTooManyRequests, err)
			return
		}
		httpx.Err(c, http.StatusBadGateway, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{"ok": true})
}
