package middleware

import (
	"io"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/cloudreve/Cloudreve/v4/application/dependency"
	"github.com/cloudreve/Cloudreve/v4/pkg/util"
	"github.com/gin-contrib/static"
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

// FrontendGzipAssets sends the files below /assets/ that the embedded zip holds deflated: as
// they are packed to clients that take gzip, instead of compressing them again on every
// request, and unpacked to the others. It comes before the gzip middleware; everything it
// does not serve goes on to FrontendFileHandler.
func FrontendGzipAssets(staticFS static.ServeFileSystem) gin.HandlerFunc {
	zipped, ok := staticFS.(interface {
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
			r.Header.Get("Range") != "" {
			c.Next()
			return
		}
		contentType := assetType(path.Ext(r.URL.Path))
		packed, modTime, ok := zipped.OpenGzip(r.URL.Path)
		if !ok || contentType == "" {
			c.Next()
			return
		}
		h := c.Writer.Header()
		h.Set("Cache-Control", "public, max-age=31536000")
		h.Set("Content-Type", contentType)
		h.Add("Vary", "Accept-Encoding")
		content := packed
		if util.AcceptsGzip(r.Header.Get("Accept-Encoding")) {
			h.Set("Content-Encoding", "gzip")
		} else {
			file, err := staticFS.Open(r.URL.Path)
			if err != nil {
				c.Next()
				return
			}
			defer file.Close()
			content = file
		}
		size, _ := content.Seek(0, io.SeekEnd)
		_, _ = content.Seek(0, io.SeekStart)
		http.ServeContent(&lengthOnSuccess{ResponseWriter: c.Writer, size: size}, r, "", modTime, content)
		c.Abort()
	}
}

// assetTypes are the types of the web app's files that Go does not know on every system.
var assetTypes = map[string]string{
	".woff2": "font/woff2",
	".ttf":   "font/ttf",
}

func assetType(ext string) string {
	if t, ok := assetTypes[ext]; ok {
		return t
	}
	return mime.TypeByExtension(ext)
}

// lengthOnSuccess sets the length of the body when it is sent; http.ServeContent leaves it out
// for an encoded body.
type lengthOnSuccess struct {
	http.ResponseWriter
	size int64
}

func (w *lengthOnSuccess) WriteHeader(code int) {
	if code == http.StatusOK {
		w.Header().Set("Content-Length", strconv.FormatInt(w.size, 10))
	}
	w.ResponseWriter.WriteHeader(code)
}
