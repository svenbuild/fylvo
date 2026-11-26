package middleware

import (
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/cloudreve/Cloudreve/v4/application/dependency"
	"github.com/cloudreve/Cloudreve/v4/pkg/util"
	"github.com/gin-gonic/gin"
)

// FrontendFileHandler frontend static file handler
func FrontendFileHandler(dep dependency.Dep) gin.HandlerFunc {
	fs := dep.ServerStaticFS()
	l := dep.Logger()

	ignoreFunc := func(c *gin.Context) {
		c.Next()
	}

	if fs == nil {
		return ignoreFunc
	}

	// read index.html
	file, err := fs.Open("/index.html")
	if err != nil {
		l.Warning("Static file \"index.html\" does not exist, it might affect the display of the homepage.")
		return ignoreFunc
	}

	fileContentBytes, err := io.ReadAll(file)
	if err != nil {
		l.Warning("Cannot read static file \"index.html\", it might affect the display of the homepage.")
		return ignoreFunc
	}
	fileContent := string(fileContentBytes)

	fileServer := http.FileServer(fs)
	return func(c *gin.Context) {
		path := c.Request.URL.Path

		// Skipping routers handled by backend
		if strings.HasPrefix(path, "/api") ||
			strings.HasPrefix(path, "/dav") ||
			strings.HasPrefix(path, "/f/") ||
			strings.HasPrefix(path, "/s/") ||
			path == "/manifest.json" {
			c.Next()
			return
		}

		// paths that do not exist and index.html both return index.html
		if (path == "/index.html") || (path == "/") || !fs.Exists("/", path) {
			// read and replace the site settings
			settingClient := dep.SettingProvider()
			siteBasic := settingClient.SiteBasic(c)
			pwaOpts := settingClient.PWA(c)
			theme := settingClient.Theme(c)
			finalHTML := util.Replace(map[string]string{
				"{siteName}":               siteBasic.Name,
				"{siteDes}":                siteBasic.Description,
				"{siteScript}":             siteBasic.Script,
				"{pwa_small_icon}":         pwaOpts.SmallIcon,
				"{pwa_medium_icon}":        pwaOpts.MediumIcon,
				"var(--defaultThemeColor)": theme.DefaultTheme,
			}, fileContent)

			c.Header("Content-Type", "text/html")
			c.Header("Cache-Control", "public, no-cache")
			c.String(200, finalHTML)
			c.Abort()
			return
		}

		if path == "/sw.js" || strings.HasPrefix(path, "/locales/") {
			c.Header("Cache-Control", "public, no-cache")
		} else if strings.HasPrefix(path, "/assets/") {
			c.Header("Cache-Control", "public, max-age=31536000")
		}

		// existing static files
		fileServer.ServeHTTP(c.Writer, c.Request)
		c.Abort()
	}
}

// FrontendGzipAssets sends the files below /assets/ to clients that take gzip as the embedded
// zip holds them, instead of compressing them again on every request. It comes before the
// gzip middleware; everything it does not serve goes on to FrontendFileHandler.
func FrontendGzipAssets(dep dependency.Dep) gin.HandlerFunc {
	zipped, ok := dep.ServerStaticFS().(interface {
		OpenGzip(name string) (io.ReadSeeker, time.Time, bool)
	})
	if !ok {
		return func(c *gin.Context) {
			c.Next()
		}
	}
	return func(c *gin.Context) {
		r := c.Request
		if !strings.HasPrefix(r.URL.Path, "/assets/") ||
			(r.Method != http.MethodGet && r.Method != http.MethodHead) ||
			!strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") ||
			r.Header.Get("Range") != "" {
			c.Next()
			return
		}
		content, modTime, ok := zipped.OpenGzip(r.URL.Path)
		if !ok {
			c.Next()
			return
		}
		size, _ := content.Seek(0, io.SeekEnd)
		_, _ = content.Seek(0, io.SeekStart)
		h := c.Writer.Header()
		h.Set("Cache-Control", "public, max-age=31536000")
		h.Set("Content-Encoding", "gzip")
		h.Add("Vary", "Accept-Encoding")
		h.Set("Content-Length", strconv.FormatInt(size, 10))
		http.ServeContent(c.Writer, r, path.Base(r.URL.Path), modTime, content)
		c.Abort()
	}
}
