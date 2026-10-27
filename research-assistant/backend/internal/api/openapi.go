package api

import (
	_ "embed"
	"net/http"

	"github.com/gin-gonic/gin"
)

//go:embed openapi.json
var openAPIDocument []byte

// serveOpenAPI exposes the machine-readable API contract. It contains no
// secrets, so it is intentionally readable without authentication.
func serveOpenAPI(c *gin.Context) {
	c.Header("Cache-Control", "public, max-age=300")
	c.Data(http.StatusOK, "application/json; charset=utf-8", openAPIDocument)
}
