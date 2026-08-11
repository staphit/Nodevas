package project

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestProjectExportRejectsWhenSaturated(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for range maxConcurrentProjectExports {
		if !acquireProjectExport(context.Background()) {
			t.Fatal("failed to reserve export slot")
		}
		defer releaseProjectExport()
	}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/projects/export", nil)
	New(nil).GetProjectExport(c)

	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusTooManyRequests)
	}
	if recorder.Header().Get("Retry-After") == "" {
		t.Fatal("saturated response should include Retry-After")
	}
}

func TestProjectExportAcquireHonorsCanceledRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if acquireProjectExport(ctx) {
		releaseProjectExport()
		t.Fatal("canceled request acquired an export slot")
	}
}

func TestProjectExportWriterStopsAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var destination bytes.Buffer
	written, err := (contextWriter{ctx: ctx, w: &destination}).Write([]byte("archive"))
	if err == nil {
		t.Fatal("canceled export write should fail")
	}
	if written != 0 || destination.Len() != 0 {
		t.Fatalf("canceled export wrote %d bytes", written)
	}

	recorder := httptest.NewRecorder()
	response := exportResponseWriter{
		ResponseWriter: recorder,
		controller:     http.NewResponseController(recorder),
		ctx:            ctx,
	}
	written, err = response.Write([]byte("download"))
	if err == nil || written != 0 || recorder.Body.Len() != 0 {
		t.Fatalf("canceled download wrote %d bytes, error %v", written, err)
	}
}
