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

func androidRouter(t *testing.T, dir string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	releases := fylvo.NewAndroidReleases(dir)
	old := androidReleases
	androidReleases = func() *fylvo.AndroidReleases { return releases }
	t.Cleanup(func() { androidReleases = old })

	r := gin.New()
	r.GET("/latest", FylvoAndroidLatest)
	r.GET("/apk/:sha256", FylvoAndroidApk)
	return r
}

func TestFylvoAndroidEndpoints(t *testing.T) {
	dir := t.TempDir()
	apk := []byte("PK fake apk content")
	sum := sha256.Sum256(apk)
	hash := hex.EncodeToString(sum[:])
	assert.NoError(t, os.WriteFile(filepath.Join(dir, hash+".apk"), apk, 0644))
	assert.NoError(t, os.WriteFile(filepath.Join(dir, fylvo.AndroidReleaseFile),
		[]byte(fmt.Sprintf(`{"version_code":3,"version_name":"1.1.0","sha256":"%s","size":%d}`, hash, len(apk))), 0644))
	r := androidRouter(t, dir)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/latest", nil))
	var body struct {
		Code int `json:"code"`
		Data struct {
			VersionCode int    `json:"version_code"`
			VersionName string `json:"version_name"`
			SHA256      string `json:"sha256"`
			Size        int64  `json:"size"`
		} `json:"data"`
	}
	assert.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, 0, body.Code)
	assert.Equal(t, 3, body.Data.VersionCode)
	assert.Equal(t, "1.1.0", body.Data.VersionName)
	assert.Equal(t, int64(len(apk)), body.Data.Size)
	assert.Equal(t, hash, body.Data.SHA256)
	assert.Equal(t, "private, no-store", rec.Header().Get("Cache-Control"))

	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/apk/"+hash, nil))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/vnd.android.package-archive", rec.Header().Get("Content-Type"))
	assert.Equal(t, apk, rec.Body.Bytes())

	req := httptest.NewRequest(http.MethodGet, "/apk/"+hash, nil)
	req.Header.Set("Range", "bytes=3-6")
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusPartialContent, rec.Code)
	assert.Equal(t, apk[3:7], rec.Body.Bytes())

	// Another checksum than the published one gets the JSON error, not a file.
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/apk/"+strings.Repeat("0", 64), nil))
	assert.Contains(t, rec.Header().Get("Content-Type"), "application/json")
	assert.Contains(t, rec.Body.String(), `"code":404`)
}

func TestFylvoAndroidWithoutReleaseIsNotFound(t *testing.T) {
	r := androidRouter(t, t.TempDir())
	for _, path := range []string{"/latest", "/apk/" + strings.Repeat("0", 64)} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		var body struct {
			Code int             `json:"code"`
			Data json.RawMessage `json:"data"`
		}
		assert.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), path)
		assert.Equal(t, 404, body.Code, path)
		assert.Empty(t, body.Data, path)
		assert.Equal(t, "private, no-store", rec.Header().Get("Cache-Control"), path)
	}
}
