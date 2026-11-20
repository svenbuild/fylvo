package middleware

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordLogger keeps every log line in memory.
type recordLogger struct{ lines *[]string }

func (r recordLogger) add(format string, v ...any) {
	*r.lines = append(*r.lines, fmt.Sprintf(format, v...))
}
func (r recordLogger) Panic(f string, v ...any)             { r.add(f, v...) }
func (r recordLogger) Error(f string, v ...any)             { r.add(f, v...) }
func (r recordLogger) Warning(f string, v ...any)           { r.add(f, v...) }
func (r recordLogger) Info(f string, v ...any)              { r.add(f, v...) }
func (r recordLogger) Debug(f string, v ...any)             { r.add(f, v...) }
func (r recordLogger) CopyWithPrefix(string) logging.Logger { return r }
func (r recordLogger) SupportColor() bool                   { return false }

// TestLoggingHidesSecrets sends requests with secrets in the address through the request logger.
func TestLoggingHidesSecrets(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var lines []string
	engine := gin.New()
	engine.ContextWithFallback = true
	engine.Use(func(c *gin.Context) {
		ctx := context.WithValue(c.Request.Context(), logging.LoggerCtx{}, logging.Logger(recordLogger{&lines}))
		c.Request = c.Request.WithContext(ctx)
	})
	engine.Use(Logging())
	engine.GET("/api/v4/file/wopi/:id", func(c *gin.Context) {
		_ = c.Error(fmt.Errorf(`Get "https://s3.example/o?X-Amz-Signature=sigsecret77": timeout`))
		c.Status(http.StatusTeapot)
	})
	engine.GET("/s/:id/:password", func(c *gin.Context) { c.Status(http.StatusFound) })
	engine.GET("/session/reset", func(c *gin.Context) { c.Status(http.StatusOK) })

	secrets := []string{"wopitoken-0b7d1c2e.Xy9AbCdEf", "sigsecret77", "sharepw55", "resetsecret99", "AbCd1234"}
	for _, target := range []string{
		"/api/v4/file/wopi/Xy?access_token=wopitoken-0b7d1c2e.Xy9AbCdEf",
		"/s/Ab/sharepw55",
		"/session/reset?id=lV&secret=resetsecret99&sign=AbCd1234%3A1767225600",
	} {
		engine.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, target, nil))
	}

	out := strings.Join(lines, "\n")
	for _, s := range secrets {
		assert.NotContains(t, out, s)
	}
	// The line is still useful: method, path, status and the parameter names.
	require.Len(t, lines, 4)
	assert.Contains(t, lines[0], "GET")
	assert.Contains(t, lines[0], "418")
	assert.Contains(t, lines[0], "/api/v4/file/wopi/Xy?access_token=***")
	assert.Contains(t, lines[1], "X-Amz-Signature=***")
	assert.Contains(t, lines[2], "/s/Ab/***")
	assert.Contains(t, lines[3], "/session/reset?id=lV&secret=***&sign=***")
}
