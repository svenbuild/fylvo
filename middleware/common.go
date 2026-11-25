package middleware

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"github.com/cloudreve/Cloudreve/v4/application/dependency"
	"github.com/cloudreve/Cloudreve/v4/pkg/auth/requestinfo"
	"github.com/cloudreve/Cloudreve/v4/pkg/cluster"
	"github.com/cloudreve/Cloudreve/v4/pkg/hashid"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/cloudreve/Cloudreve/v4/pkg/request"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/cloudreve/Cloudreve/v4/pkg/util"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"
)

// HashID converts the HashID of the given object to the real ID
func HashID(IDType int) gin.HandlerFunc {
	return func(c *gin.Context) {
		dep := dependency.FromContext(c)
		if c.Param("id") != "" {
			id, err := dep.HashIDEncoder().Decode(c.Param("id"), IDType)
			if err == nil {
				util.WithValue(c, hashid.ObjectIDCtx{}, id)
				c.Next()
				return
			}
			c.JSON(200, serializer.ParamErr(c, "Failed to parse object ID", err))
			c.Abort()
			return

		}
		c.Next()
	}
}

// IsFunctionEnabled blocks access when the feature is not enabled
func IsFunctionEnabled(check func(c *gin.Context) bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !check(c) {
			c.JSON(200, serializer.ErrWithDetails(c, serializer.CodeFeatureNotEnabled, "This feature is not enabled", nil))
			c.Abort()
			return
		}

		c.Next()
	}
}

// CacheControl disables client caching
func CacheControl() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "private, no-cache")
	}
}

func Sandbox() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Content-Security-Policy", "sandbox")
	}
}

// StaticResourceCache uses the static resource cache policy
func StaticResourceCache(dep dependency.Dep) gin.HandlerFunc {
	settings := dep.SettingProvider()
	return func(c *gin.Context) {
		c.Header("Cache-Control", fmt.Sprintf("public, max-age=%d", settings.PublicResourceMaxAge(c)))

	}
}

// InitializeHandling is added at the beginning of handler chain, it did following setups:
// 1. Inject dependency manager into request context
// 2. Generate and inject correlation ID for diagnostic.
func InitializeHandling(dep dependency.Dep) gin.HandlerFunc {
	return func(c *gin.Context) {
		clientIp := c.ClientIP()
		if idx := strings.Index(clientIp, ","); idx > 0 {
			clientIp = clientIp[:idx]
		}

		reqInfo := &requestinfo.RequestInfo{
			IP:        clientIp,
			Host:      c.Request.Host,
			UserAgent: c.Request.UserAgent(),
			ClientID:  c.GetHeader(request.ClientIDHeader),
		}
		cid := uuid.FromStringOrNil(c.GetHeader(request.CorrelationHeader))
		if cid == uuid.Nil {
			cid = uuid.Must(uuid.NewV4())
		}

		l := dep.Logger().CopyWithPrefix(fmt.Sprintf("[Cid: %s]", cid))
		ctx := dep.ForkWithLogger(c.Request.Context(), l)
		ctx = context.WithValue(ctx, logging.CorrelationIDCtx{}, cid)
		ctx = context.WithValue(ctx, requestinfo.RequestInfoCtx{}, reqInfo)
		ctx = context.WithValue(ctx, logging.LoggerCtx{}, l)
		if id := c.Param("nodeId"); id != "" {
			ctx = context.WithValue(ctx, cluster.SlaveNodeIDCtx{}, id)
		} else {
			ctx = context.WithValue(ctx, cluster.SlaveNodeIDCtx{}, c.GetHeader(request.SlaveNodeIDHeader))
		}
		c.Request = c.Request.WithContext(ctx)

		c.Next()
	}
}

// InitializeHandlingSlave retrieves coll correlation ID and other metadata from request header
func InitializeHandlingSlave() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := context.WithValue(c.Request.Context(), cluster.MasterSiteIDCtx{}, c.GetHeader(request.SiteIDHeader))
		ctx = context.WithValue(ctx, cluster.MasterSiteUrlCtx{}, c.GetHeader(request.SiteURLHeader))
		ctx = context.WithValue(ctx, cluster.MasterSiteVersionCtx{}, c.GetHeader(request.SiteVersionHeader))
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

// Logging logs incoming request info
func Logging() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Start timer
		start := time.Now()
		path := logPath(c.Request)
		raw := c.Request.URL.RawQuery

		// Process request
		c.Next()

		if raw != "" {
			path = path + "?" + raw
		}

		l := logging.FromContext(c)
		logging.Request(l, true, c.Writer.Status(), c.Request.Method, c.ClientIP(), path,
			c.Errors.ByType(gin.ErrorTypePrivate).String(), start)
	}
}

// logPathEscaper escapes the characters that would shift the query or break the log line.
var logPathEscaper = strings.NewReplacer("%", "%25", "?", "%3F", "#", "%23", "\n", "%0A", "\r", "%0D")

// logPath returns the request path for a log line. It is the decoded path, so the masking in
// logging.RedactURL sees the segments as they were routed (/%73/Ab/pw is /s/Ab/pw).
func logPath(r *http.Request) string {
	return logPathEscaper.Replace(r.URL.Path)
}

// Recovery replaces gin.Recovery, whose dump of the raw request on a lost connection would print
// the query with its secrets and the Cookie header. A lost connection gives a warning, any other
// panic an error with the stack and the answer 500.
func Recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			err := recover()
			if err == nil {
				return
			}
			if err == http.ErrAbortHandler {
				// Fylvo: a handler that gives up in the middle of an answer. The server
				// then cuts the connection, so the client does not take what it got so
				// far for the whole answer.
				panic(err)
			}
			l := logging.FromContext(c)
			path := logging.RedactURL(logPath(c.Request))
			if ne, ok := err.(*net.OpError); ok && connectionLost(ne) {
				l.Warning("Connection lost in %s %s: %s", c.Request.Method, path, ne)
				_ = c.Error(ne)
				c.Abort()
				return
			}
			l.Error("Panic recovered in %s %s: %v\n%s", c.Request.Method, path, err, debug.Stack())
			c.AbortWithStatus(http.StatusInternalServerError)
		}()
		c.Next()
	}
}

// connectionLost reports a write to a client that has gone, as gin.Recovery detects it.
func connectionLost(err *net.OpError) bool {
	var se *os.SyscallError
	if !errors.As(err, &se) {
		return false
	}
	msg := strings.ToLower(se.Error())
	return strings.Contains(msg, "broken pipe") || strings.Contains(msg, "connection reset by peer")
}

func ContentCORS() gin.HandlerFunc {
	return cors.New(cors.Config{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "HEAD", "OPTIONS"},
		AllowHeaders:     []string{"Range", "If-Range", "Authorization", "Content-Type"},
		ExposeHeaders:    []string{"Content-Range", "Accept-Ranges", "Content-Length", "Content-Disposition", "Content-Disposition", "ETag"},
		AllowCredentials: false,
	})
}
