package middleware

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"regexp"
	"strings"

	"github.com/cloudreve/Cloudreve/v4/pkg/setting"
	"github.com/gin-gonic/gin"
)

// Fylvo: the Craft editors (see pkg/setting/fylvo_craft.go) under setting.CraftRoute. Only
// files below the folder are served, and a path that is not there is 404, not the web app.
// scripts/fetch-craft.sh stores the big files compressed, as name.br and name.gz; a browser
// gets the encoding it takes, one that takes neither gets the .gz unpacked.

// craftHashed matches the names the build gave a content hash ("app-web-0123456789abcdef.js");
// such a file never changes, every other one is checked again.
var craftHashed = regexp.MustCompile(`[-_][0-9a-f]{16}(_bg)?\.[a-z]+$`)

// craftInlineScript finds the scripts written into a page (no src).
var craftInlineScript = regexp.MustCompile(`(?s)<script(\s[^>]*)?>(.*?)</script>`)

// craftPolicy is the Content-Security-Policy of an editor's page. The editors run on this
// server's origin and so could reach the account; the policy lets only their own files run
// and talk to this server.
const craftPolicy = "default-src 'self'; script-src 'self' 'wasm-unsafe-eval'%s; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: blob:; font-src 'self' data:; connect-src 'self'; worker-src 'self' blob:; " +
	"object-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'"

// CraftFiles serves the Craft editors from dir.
func CraftFiles(dir string) gin.HandlerFunc {
	// The policy of every app's page, worked out once.
	policies := map[string]string{}
	if root, err := os.OpenRoot(dir); err == nil {
		apps, _ := fs.ReadDir(root.FS(), ".")
		for _, app := range apps {
			if app.IsDir() {
				policies[app.Name()] = craftPagePolicy(root, app.Name()+"/index.html")
			}
		}
		_ = root.Close()
	}
	return func(c *gin.Context) {
		if !strings.HasPrefix(c.Request.URL.Path, setting.CraftRoute) {
			c.Next()
			return
		}
		c.Abort()
		if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
			c.Status(http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(c.Request.URL.Path, setting.CraftRoute)
		if name == "" || strings.HasSuffix(name, "/") {
			name += "index.html"
		}
		if strings.Contains(name, "\\") || !fs.ValidPath(name) {
			c.Status(http.StatusNotFound)
			return
		}
		root, err := os.OpenRoot(dir)
		if err != nil {
			c.Status(http.StatusNotFound)
			return
		}
		defer root.Close()
		if info, err := root.Stat(name); err == nil && info.IsDir() {
			c.Redirect(http.StatusMovedPermanently, c.Request.URL.Path+"/")
			return
		}

		file, encoding := openCraftFile(root, name, c.GetHeader("Accept-Encoding"))
		if file == nil {
			c.Status(http.StatusNotFound)
			return
		}
		defer file.Close()

		h := c.Writer.Header()
		h.Set("Content-Type", craftType(name))
		h.Set("Vary", "Accept-Encoding")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		if craftHashed.MatchString(name) {
			h.Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			h.Set("Cache-Control", "no-cache")
		}
		if path.Ext(name) == ".html" {
			app, _, _ := strings.Cut(name, "/")
			policy, ok := policies[app]
			if !ok {
				policy = craftPagePolicy(nil, "")
			}
			h.Set("Content-Security-Policy", policy)
		}

		switch encoding {
		case "gunzip":
			unpacked, err := gzip.NewReader(file)
			if err != nil {
				c.Status(http.StatusInternalServerError)
				return
			}
			c.Status(http.StatusOK)
			if c.Request.Method == http.MethodGet {
				_, _ = io.Copy(c.Writer, unpacked)
			}
		default:
			if encoding != "" {
				h.Set("Content-Encoding", encoding)
			}
			info, err := file.Stat()
			if err != nil {
				c.Status(http.StatusInternalServerError)
				return
			}
			http.ServeContent(c.Writer, c.Request, "", info.ModTime(), file)
		}
	}
}

// openCraftFile opens name in the best form the browser takes. The encoding is "br" or
// "gzip" for a compressed file, "gunzip" for a .gz to unpack, "" for the file as it is.
func openCraftFile(root *os.Root, name, acceptEncoding string) (*os.File, string) {
	type form struct{ suffix, encoding string }
	var tries []form
	if acceptsEncoding(acceptEncoding, "br") {
		tries = append(tries, form{".br", "br"})
	}
	if acceptsEncoding(acceptEncoding, "gzip") {
		tries = append(tries, form{".gz", "gzip"})
	}
	tries = append(tries, form{"", ""}, form{".gz", "gunzip"})
	for _, try := range tries {
		if file, err := root.Open(name + try.suffix); err == nil {
			if info, err := file.Stat(); err == nil && info.Mode().IsRegular() {
				return file, try.encoding
			}
			_ = file.Close()
		}
	}
	return nil, ""
}

// acceptsEncoding tells whether an Accept-Encoding header takes the encoding.
func acceptsEncoding(header, encoding string) bool {
	for _, part := range strings.Split(header, ",") {
		token, params, _ := strings.Cut(part, ";")
		if strings.EqualFold(strings.TrimSpace(token), encoding) {
			q := strings.ReplaceAll(strings.TrimSpace(params), " ", "")
			return q != "q=0" && q != "q=0.0" && q != "q=0.00" && q != "q=0.000"
		}
	}
	return false
}

func craftType(name string) string {
	switch path.Ext(name) {
	case ".wasm":
		return "application/wasm"
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".html":
		return "text/html; charset=utf-8"
	}
	if t := mime.TypeByExtension(path.Ext(name)); t != "" {
		return t
	}
	return "application/octet-stream"
}

// craftPagePolicy is the policy for the page at name, with the hashes of the scripts written
// into it allowed to run. Without a page no such script runs.
func craftPagePolicy(root *os.Root, name string) string {
	hashes := ""
	if root == nil {
		return strings.Replace(craftPolicy, "%s", hashes, 1)
	}
	if file, encoding := openCraftFile(root, name, ""); file != nil {
		var page io.Reader = file
		if encoding == "gunzip" {
			if unpacked, err := gzip.NewReader(file); err == nil {
				page = unpacked
			}
		}
		content, _ := io.ReadAll(io.LimitReader(page, 1<<20))
		_ = file.Close()
		for _, match := range craftInlineScript.FindAllSubmatch(content, -1) {
			if strings.Contains(string(match[1]), "src=") {
				continue
			}
			sum := sha256.Sum256(match[2])
			hashes += " 'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
		}
	}
	return strings.Replace(craftPolicy, "%s", hashes, 1)
}
