package fsbrowse

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// ginContext builds the *gin.Context a handler takes, for the tests that call
// one directly instead of driving it through the router. Tests that need path
// parameters must set c.Params themselves; these callers have none.
func ginContext(response http.ResponseWriter, request *http.Request) *gin.Context {
	c, _ := gin.CreateTestContext(response)
	c.Request = request
	return c
}
