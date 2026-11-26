package middleware

import (
	"compress/gzip"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/cloudreve/Cloudreve/v4/application/statics"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestFrontendGzipAssets(t *testing.T) {
	gin.SetMode(gin.TestMode)
	l := logging.NewConsoleLogger(logging.LevelError)
	embedded := statics.NewStaticFS(l)
	staticFS, err := statics.NewServerStaticFS(l, embedded, false)
	require.NoError(t, err)
	r := gin.New()
	r.Use(FrontendGzipAssets(staticFS))
	r.NoRoute(func(c *gin.Context) { c.String(http.StatusTeapot, "next") })

	scripts, err := fs.Glob(embedded, "assets/build/assets/*.js")
	require.NoError(t, err)
	require.NotEmpty(t, scripts)
	target := strings.TrimPrefix(scripts[0], "assets/build")
	plain, err := fs.ReadFile(embedded, scripts[0])
	require.NoError(t, err)

	do := func(method, encoding string, header ...string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, nil)
		if encoding != "" {
			req.Header.Set("Accept-Encoding", encoding)
		}
		for i := 0; i+1 < len(header); i += 2 {
			req.Header.Set(header[i], header[i+1])
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	w := do(http.MethodGet, "gzip, br")
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "gzip", w.Header().Get("Content-Encoding"))
	require.Equal(t, "Accept-Encoding", w.Header().Get("Vary"))
	require.Equal(t, "text/javascript; charset=utf-8", w.Header().Get("Content-Type"))
	require.Equal(t, strconv.Itoa(w.Body.Len()), w.Header().Get("Content-Length"))
	zr, err := gzip.NewReader(w.Body)
	require.NoError(t, err)
	unpacked, err := io.ReadAll(zr)
	require.NoError(t, err)
	require.Equal(t, plain, unpacked)
	modified := w.Header().Get("Last-Modified")

	for _, encoding := range []string{"gzip;q=0", ""} {
		w = do(http.MethodGet, encoding)
		require.Equal(t, http.StatusOK, w.Code, encoding)
		require.Empty(t, w.Header().Get("Content-Encoding"), encoding)
		require.Equal(t, "Accept-Encoding", w.Header().Get("Vary"), encoding)
		require.Equal(t, plain, w.Body.Bytes(), encoding)
	}

	w = do(http.MethodHead, "gzip")
	require.Equal(t, http.StatusOK, w.Code)
	require.NotEmpty(t, w.Header().Get("Content-Length"))
	require.Zero(t, w.Body.Len())

	w = do(http.MethodGet, "gzip", "If-Modified-Since", modified)
	require.Equal(t, http.StatusNotModified, w.Code)
	require.Empty(t, w.Header().Get("Content-Length"))
	require.Zero(t, w.Body.Len())

	w = do(http.MethodGet, "gzip", "If-Unmodified-Since", "Mon, 01 Jan 2001 00:00:00 GMT")
	require.Equal(t, http.StatusPreconditionFailed, w.Code)
	require.Empty(t, w.Header().Get("Content-Length"))

	// A range is part of the file as it is, also for a client that takes gzip.
	w = do(http.MethodGet, "gzip", "Range", "bytes=0-10")
	require.Equal(t, http.StatusPartialContent, w.Code)
	require.Empty(t, w.Header().Get("Content-Encoding"))
	require.Equal(t, "bytes 0-10/"+strconv.Itoa(len(plain)), w.Header().Get("Content-Range"))
	require.Equal(t, plain[:11], w.Body.Bytes())

	// Vary is added once, next to the one of CORS.
	cors := gin.New()
	cors.Use(func(c *gin.Context) { c.Header("Vary", "Origin") }, FrontendGzipAssets(staticFS))
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Header.Add("Accept-Encoding", "br")
	req.Header.Add("Accept-Encoding", "gzip")
	w = httptest.NewRecorder()
	cors.ServeHTTP(w, req)
	require.Equal(t, "gzip", w.Header().Get("Content-Encoding"))
	require.Equal(t, []string{"Origin", "Accept-Encoding"}, w.Header().Values("Vary"))

	// A path with dots and a file that is not there go on.
	target = "/assets/../index.html"
	require.Equal(t, http.StatusTeapot, do(http.MethodGet, "gzip").Code)
	target = "/assets/missing.js"
	require.Equal(t, http.StatusTeapot, do(http.MethodGet, "gzip").Code)
	require.Equal(t, http.StatusTeapot, do(http.MethodGet, "").Code)
}
