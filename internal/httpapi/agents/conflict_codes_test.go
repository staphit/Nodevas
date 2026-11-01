package agents

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"nodevas/internal/agents"
)

// Three different 409s ask for three different next moves; each says
// which one it is.
func TestConflictsSayWhichConflict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for err, want := range map[error]string{
		agents.ErrLoopDetected: "LOOP_DETECTED",
		agents.ErrNotHeld:      "NOT_HELD",
		agents.ErrAskBusy:      "CONFLICT",
	} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/agents/messages", nil)
		fail(c, fmt.Errorf("wrapped: %w", err))
		var body struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(recorder.Body.Bytes(), &body)
		if recorder.Code != http.StatusConflict || body.Code != want {
			t.Errorf("%v: %d %q, want 409 %q", err, recorder.Code, body.Code, want)
		}
	}
}
