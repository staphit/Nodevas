package httpx

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"nodevas/internal/auth"
	"nodevas/internal/locale"
	"nodevas/internal/project"
)

// DenyProjectAccess answers a request for a project its actor may not read
// (internal/members): 403 PROJECT_ACCESS_DENIED. Every route that refuses a
// project for that reason answers with this, so no two refusals differ in a
// way that would tell one restricted project from another.
func DenyProjectAccess(c *gin.Context) {
	Coded(c, http.StatusForbidden, "PROJECT_ACCESS_DENIED",
		locale.Errorf("you are not a member of this project", "你不是這個專案的成員"), nil)
}

// RefuseUnknownProject answers a request naming a project that does not
// resolve (err). With membership on, a caller who is not an administrator
// is answered as for a project they may not read (DenyProjectAccess): a 400
// with the reason, next to a 403 for a restricted project, would tell which
// names exist. Otherwise, and for an administrator, 400 with the reason. A
// request with no actor attached is not an administrator's.
//
// A lookup the disk could not answer (project.IsLookupFault) is neither:
// 503 PROJECT_LOOKUP_FAILED with Retry-After, so the client tries again
// instead of telling its user the project is gone or not theirs.
func RefuseUnknownProject(c *gin.Context, err error, membership bool) {
	if project.IsLookupFault(err) {
		ProjectLookupFailed(c, err)
		return
	}
	if membership && auth.RequireAdmin(c.Request) != nil {
		DenyProjectAccess(c)
		return
	}
	Err(c, http.StatusBadRequest, err)
}

// ProjectLookupFailed answers a request whose project could not be looked
// up (project.IsLookupFault): 503 PROJECT_LOOKUP_FAILED, retry in a few
// seconds. The reason goes to the log, not to the caller.
func ProjectLookupFailed(c *gin.Context, err error) {
	c.Header("Retry-After", "5")
	Coded(c, http.StatusServiceUnavailable, "PROJECT_LOOKUP_FAILED", err, nil)
}

// DenyProjectWrite answers a change to a project its actor may only read:
// 403 PROJECT_READ_ONLY.
func DenyProjectWrite(c *gin.Context) {
	Coded(c, http.StatusForbidden, "PROJECT_READ_ONLY",
		locale.Errorf("you can view this project but not change it", "你可以檢視這個專案，但不能修改"), nil)
}
