package middleware

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/fatih/color"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

// TestLoggingHidesSecrets sends requests with secrets in the address through Recovery and the
// request logger, in the order of routers/router.go, and reads the console log.
func TestLoggingHidesSecrets(t *testing.T) {
	var buf bytes.Buffer
	old := color.Output
	color.Output = &buf
	defer func() { color.Output = old }()

	gin.SetMode(gin.TestMode)
	l := logging.NewConsoleLogger(logging.LevelInformational)
	engine := gin.New()
	engine.ContextWithFallback = true
	engine.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), logging.LoggerCtx{}, l))
	})
	engine.Use(Recovery())
	engine.Use(Logging())
	engine.GET("/api/v4/file/wopi/:id", func(c *gin.Context) {
		_ = c.Error(fmt.Errorf(`Get "https://s3.example/o?X-Amz-Signature=sigsecret77": timeout`))
		c.Status(http.StatusTeapot)
	})
	engine.GET("/s/:id/:password", func(c *gin.Context) { c.Status(http.StatusFound) })
	engine.GET("/session/reset", func(c *gin.Context) { c.Status(http.StatusOK) })
	engine.GET("/api/v4/file/content/:id/:speed/:name", func(c *gin.Context) { c.Status(http.StatusOK) })
	engine.GET("/api/v4/fylvo/connected/start/:ticket", func(c *gin.Context) { c.Status(http.StatusOK) })
	engine.POST("/api/v4/fylvo/connected/start/:ticket", func(c *gin.Context) { c.Status(http.StatusSeeOther) })
	engine.GET("/api/v4/fylvo/connected/thumb/:grant/:name", func(c *gin.Context) { c.Status(http.StatusOK) })
	engine.GET("/s/:id/:password/panic", func(c *gin.Context) { panic("boom") })
	engine.GET("/s/:id/:password/gone", func(c *gin.Context) {
		panic(&net.OpError{Op: "write", Err: os.NewSyscallError("write", errors.New("broken pipe"))})
	})

	for _, target := range []string{
		"/api/v4/file/wopi/Xy?access_token=wopitoken-0b7d1c2e.Xy9AbCdEf",
		"/s/Ab/sharepw55",
		"/session/reset?id=lV&secret=resetsecret99&sign=AbCd1234%3A1767225600",
		// A "?" in the file name must not shift the start of the query.
		"/api/v4/file/content/Xy/0/a%3Fb.pdf?sign=shortsig",
		"/api/v4/file/content/Xy/0/a%23b.pdf?sign=shortsig",
		"/s/Ab/panicpw/panic?access_token=panictoken",
		// An encoded fixed segment is routed like /s/..., so it is masked like it.
		"/%73/Ab/encodedpw",
		"/s/Ab/gonepw/gone",
		// Fylvo: the ticket of connecting a storage from an app, also spelled with escapes.
		"/api/v4/fylvo/connected/start/ticketAbCdEf0123456789-_ticketAbCdEf0123456",
		"/api/v4/fylvo/connected/st%61rt/ticketEscaped0123456789-_ticketAbCdEf012345",
		// Fylvo: the grant in the address of a preview, also spelled with escapes.
		"/api/v4/fylvo/connected/thumb/grantOfAPreviewAbCdEf0123456789-_AbCdEf/thumb.jpg",
		"/api/v4/fylvo/connected/th%75mb/grantEscapedAbCdEf0123456789-_AbCdEf/thumb.jpg",
	} {
		engine.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, target, nil))
	}
	engine.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost,
		"/api/v4/fylvo/connected/start/ticketPosted0123456789-_ticketAbCdEf0123456", nil))

	out := buf.String()
	for _, s := range []string{"wopitoken-0b7d1c2e.Xy9AbCdEf", "sigsecret77", "sharepw55", "resetsecret99",
		"AbCd1234", "shortsig", "panictoken", "panicpw", "encodedpw", "gonepw",
		"ticketAbCdEf", "ticketEscaped", "ticketPosted", "grantOfAPreview", "grantEscaped"} {
		assert.NotContains(t, out, s)
	}
	// The lines stay useful: method, status, path and the parameter names.
	for _, s := range []string{
		`418`, `GET`, `"/api/v4/file/wopi/Xy?access_token=***"`,
		`X-Amz-Signature=***`,
		`"/s/Ab/***"`,
		`"/session/reset?id=lV&secret=***&sign=***"`,
		`"/api/v4/file/content/Xy/0/a%3Fb.pdf?sign=***"`,
		`"/api/v4/file/content/Xy/0/a%23b.pdf?sign=***"`,
		`Panic recovered in GET /s/Ab/***/panic: boom`,
		`Connection lost in GET /s/Ab/***/gone:`,
		`GET`, `"/api/v4/fylvo/connected/start/***"`,
		`"/api/v4/fylvo/connected/thumb/***/thumb.jpg"`,
	} {
		assert.Contains(t, out, s)
	}
}
