package middleware

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const craftTestScript = "\nimport init from './app-web-0123456789abcdef.js';\n"

func craftTestDir(t *testing.T) string {
	dir := t.TempDir()
	app := filepath.Join(dir, "wordcraft")
	require.NoError(t, os.MkdirAll(filepath.Join(app, "models"), 0o755))
	page := `<html><head><script type="module">` + craftTestScript + `</script>` +
		`<link rel="modulepreload" href="./app-web-0123456789abcdef.js"></head></html>`
	require.NoError(t, os.WriteFile(filepath.Join(app, "index.html"), []byte(page), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(app, "app-web-0123456789abcdef_bg.wasm.br"), []byte("brotli"), 0o644))
	var packed bytes.Buffer
	zw := gzip.NewWriter(&packed)
	_, _ = zw.Write([]byte("wasm bytes"))
	require.NoError(t, zw.Close())
	require.NoError(t, os.WriteFile(filepath.Join(app, "app-web-0123456789abcdef_bg.wasm.gz"), packed.Bytes(), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(app, "models", "text.onnx"), []byte("model"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("outside"), 0o644))
	return dir
}

func craftRequest(handler gin.HandlerFunc, method, target, encoding string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(handler)
	r.NoRoute(func(c *gin.Context) { c.String(http.StatusTeapot, "app") })
	req := httptest.NewRequest(method, target, nil)
	if encoding != "" {
		req.Header.Set("Accept-Encoding", encoding)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestCraftFilesEncodings(t *testing.T) {
	handler := CraftFiles(craftTestDir(t))
	wasm := "/craft/wordcraft/app-web-0123456789abcdef_bg.wasm"

	rec := craftRequest(handler, http.MethodGet, wasm, "gzip, deflate, br")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "br", rec.Header().Get("Content-Encoding"))
	assert.Equal(t, "brotli", rec.Body.String())
	assert.Equal(t, "application/wasm", rec.Header().Get("Content-Type"))
	assert.Equal(t, "Accept-Encoding", rec.Header().Get("Vary"))
	assert.Contains(t, rec.Header().Get("Cache-Control"), "immutable")

	// Plain HTTP: browsers do not offer br there.
	rec = craftRequest(handler, http.MethodGet, wasm, "gzip, deflate, br;q=0")
	assert.Equal(t, "gzip", rec.Header().Get("Content-Encoding"))

	rec = craftRequest(handler, http.MethodGet, wasm, "")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, rec.Header().Get("Content-Encoding"))
	assert.Equal(t, "wasm bytes", rec.Body.String())

	// A file without a hash in its name is checked again.
	rec = craftRequest(handler, http.MethodGet, "/craft/wordcraft/models/text.onnx", "br")
	assert.Equal(t, "model", rec.Body.String())
	assert.Equal(t, "no-cache", rec.Header().Get("Cache-Control"))
}

func TestCraftFilesPage(t *testing.T) {
	handler := CraftFiles(craftTestDir(t))
	rec := craftRequest(handler, http.MethodGet, "/craft/wordcraft/", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "text/html; charset=utf-8", rec.Header().Get("Content-Type"))
	assert.Equal(t, "no-cache", rec.Header().Get("Cache-Control"))
	sum := sha256.Sum256([]byte(craftTestScript))
	policy := rec.Header().Get("Content-Security-Policy")
	assert.Contains(t, policy, "script-src 'self' 'wasm-unsafe-eval' 'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"';")
	assert.Contains(t, policy, "connect-src 'self'")
	assert.Contains(t, policy, "frame-ancestors 'self'")

	rec = craftRequest(handler, http.MethodGet, "/craft/wordcraft", "")
	assert.Equal(t, http.StatusMovedPermanently, rec.Code)
	assert.Equal(t, "/craft/wordcraft/", rec.Header().Get("Location"))
}

func TestCraftFilesRefuses(t *testing.T) {
	handler := CraftFiles(craftTestDir(t))
	for _, target := range []string{
		"/craft/wordcraft/missing.js",
		"/craft/wordcraft/../secret.txt",
		"/craft/%2e%2e/secret.txt",
		"/craft/wordcraft/..%5csecret.txt",
		"/craft/wordcraft/models/../../secret.txt",
	} {
		rec := craftRequest(handler, http.MethodGet, target, "")
		// Never the web app, never a file outside the folder.
		assert.NotEqual(t, http.StatusTeapot, rec.Code, target)
		assert.NotEqual(t, "outside", rec.Body.String(), target)
	}
	assert.Equal(t, http.StatusNotFound, craftRequest(handler, http.MethodGet, "/craft/wordcraft/missing.js", "").Code)
	assert.Equal(t, http.StatusMethodNotAllowed, craftRequest(handler, http.MethodPost, "/craft/wordcraft/", "").Code)

	// Other paths go on to the web app.
	assert.Equal(t, http.StatusTeapot, craftRequest(handler, http.MethodGet, "/home", "").Code)

	// Without the folder nothing is served.
	missing := CraftFiles(filepath.Join(t.TempDir(), "none"))
	assert.Equal(t, http.StatusNotFound, craftRequest(missing, http.MethodGet, "/craft/wordcraft/", "").Code)
}
