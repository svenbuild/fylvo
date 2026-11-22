package controllers

import (
	"fmt"
	"net/http"

	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/cloudreve/Cloudreve/v4/service/fylvo"
	"github.com/gin-gonic/gin"
)

// desktopReleases is where the published desktop releases are read from; tests replace it.
var desktopReleases = fylvo.DefaultDesktopReleases

// FylvoDesktopLatest tells Fylvo Desktop which version is published for its platform (Fylvo,
// in-app updates).
func FylvoDesktopLatest(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	release, err := desktopReleases().Latest(c.Param("platform"))
	if err != nil {
		c.JSON(200, serializer.Err(c, err))
		return
	}
	c.JSON(200, serializer.Response{Data: release})
}

// FylvoDesktopPackage sends the published package of a platform, but only under the checksum
// FylvoDesktopLatest named. The app checks size and checksum, and Windows the signature.
func FylvoDesktopPackage(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	release, f, err := desktopReleases().OpenPackage(c.Param("platform"), c.Param("sha256"))
	if err != nil {
		c.JSON(200, serializer.Err(c, err))
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		c.JSON(200, serializer.Err(c, fylvo.ErrNoDesktopRelease))
		return
	}
	h := c.Writer.Header()
	h.Set("Content-Type", "application/octet-stream")
	// The file name is checked against [A-Za-z0-9._-] when release.json is read.
	h.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, release.FileName))
	http.ServeContent(c.Writer, c.Request, "", info.ModTime(), f)
}
