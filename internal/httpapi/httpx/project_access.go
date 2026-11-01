package httpx

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"nodevas/internal/locale"
)

// DenyProjectAccess answers a request for a project its actor may not read
// (internal/members): 403 PROJECT_ACCESS_DENIED. Every route that refuses a
// project for that reason answers with this, so no two refusals differ in a
// way that would tell one restricted project from another.
func DenyProjectAccess(c *gin.Context) {
	Coded(c, http.StatusForbidden, "PROJECT_ACCESS_DENIED",
		locale.Errorf("you are not a member of this project", "你不是這個專案的成員"), nil)
}

// DenyProjectWrite answers a change to a project its actor may only read:
// 403 PROJECT_READ_ONLY.
func DenyProjectWrite(c *gin.Context) {
	Coded(c, http.StatusForbidden, "PROJECT_READ_ONLY",
		locale.Errorf("you can view this project but not change it", "你可以檢視這個專案，但不能修改"), nil)
}
