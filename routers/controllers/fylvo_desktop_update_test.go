package controllers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudreve/Cloudreve/v4/service/fylvo"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func desktopRouter(t *testing.T, dir string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	releases := fylvo.NewDesktopReleases(dir)
	old := desktopReleases
	desktopReleases = func() *fylvo.DesktopReleases { return releases }
	t.Cleanup(func() { desktopReleases = old })

	r := gin.New()
	r.GET("/desktop/:platform/latest", FylvoDesktopLatest)
	r.GET("/desktop/:platform/package/:sha256", FylvoDesktopPackage)
	return r
}

func TestFylvoDesktopEndpoints(t *testing.T) {
	dir := t.TempDir()
	pkg := []byte("PK fake msix content")
	sum := sha256.Sum256(pkg)
	hash := hex.EncodeToString(sum[:])
	platformDir := filepath.Join(dir, "windows-x64")
	assert.NoError(t, os.MkdirAll(platformDir, 0755))
	assert.NoError(t, os.WriteFile(filepath.Join(platformDir, hash+".pkg"), pkg, 0644))
	assert.NoError(t, os.WriteFile(filepath.Join(platformDir, "release.json"),
		[]byte(fmt.Sprintf(`{"version":"0.3.0.0","sha256":"%s","size":%d,"file_name":"Fylvo-Desktop-0.3.0.0-x64.msix"}`,
			hash, len(pkg))), 0644))
	r := desktopRouter(t, dir)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/desktop/windows-x64/latest", nil))
	var body struct {
		Code int                  `json:"code"`
		Data fylvo.DesktopRelease `json:"data"`
	}
	assert.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, 0, body.Code)
	assert.Equal(t, fylvo.DesktopRelease{Version: "0.3.0.0", SHA256: hash, Size: int64(len(pkg)),
		FileName: "Fylvo-Desktop-0.3.0.0-x64.msix"}, body.Data)
	assert.Equal(t, "private, no-store", rec.Header().Get("Cache-Control"))

	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/desktop/windows-x64/package/"+hash, nil))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, `attachment; filename="Fylvo-Desktop-0.3.0.0-x64.msix"`, rec.Header().Get("Content-Disposition"))
	assert.Equal(t, pkg, rec.Body.Bytes())

	// Another platform has nothing published, an unknown one is the same "not found".
	for _, path := range []string{"/desktop/linux-x64/latest", "/desktop/mac-x64/latest",
		"/desktop/linux-x64/package/" + hash, "/desktop/windows-x64/package/" + strings.Repeat("0", 64)} {
		rec = httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		assert.Contains(t, rec.Header().Get("Content-Type"), "application/json", path)
		assert.Contains(t, rec.Body.String(), `"code":404`, path)
	}
}
