package controllers

import (
	"fmt"
	"net/http"

	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/cloudreve/Cloudreve/v4/service/fylvo"
	"github.com/gin-gonic/gin"
)

// androidReleases is where the published Android release is read from; tests replace it.
var androidReleases = fylvo.DefaultAndroidReleases

// FylvoAndroidLatest tells the Android app which version is published (Fylvo, in-app updates).
func FylvoAndroidLatest(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	release, err := androidReleases().Latest()
	if err != nil {
		c.JSON(200, serializer.Err(c, err))
		return
	}
	c.JSON(200, serializer.Response{Data: release})
}

// FylvoAndroidApk sends the published APK, but only under the checksum FylvoAndroidLatest
// named. The app checks size, checksum, package and signer before it opens the installer, and
// Android installs it only over an app signed with the same key.
func FylvoAndroidApk(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	release, f, err := androidReleases().OpenApk(c.Param("sha256"))
	if err != nil {
		c.JSON(200, serializer.Err(c, err))
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		c.JSON(200, serializer.Err(c, fylvo.ErrNoAndroidRelease))
		return
	}
	h := c.Writer.Header()
	h.Set("Content-Type", "application/vnd.android.package-archive")
	h.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="fylvo-%d.apk"`, release.VersionCode))
	http.ServeContent(c.Writer, c.Request, "", info.ModTime(), f)
}
