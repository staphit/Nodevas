package system

import (
	"github.com/gin-gonic/gin"

	"nodevas/internal/httpapi/httpx"
)

// Register wires sign-in, notifications, the audit trail and document export.
func (a *API) Register(api *gin.RouterGroup) {
	api.GET("/auth/status", a.getAuthStatus)
	api.POST("/auth/otp/request", a.postRequestOTP)
	api.POST("/auth/login", a.postLogin)
	api.POST("/auth/logout", a.postLogout)
	api.GET("/auth/step-up", a.getStepUp)
	api.POST("/auth/step-up", a.postStepUp)
	api.POST("/auth/step-up/request", a.postStepUpRequest)
	api.POST("/export", a.postExport)

	admin := api.Group("")
	admin.Use(httpx.RequireAdmin)
	admin.GET("/notify/settings", a.getNotifySettings)
	admin.PUT("/notify/settings", a.putNotifySettings)
	admin.POST("/notify/test", a.postNotifyTest)
	admin.GET("/audit", a.getAudit)
	admin.GET("/audit/health", a.getAuditHealth)
	admin.POST("/audit/health/acknowledge", a.postAuditHealthAcknowledge)
	admin.GET("/audit/limiter", a.getAuditLimiter)
}
