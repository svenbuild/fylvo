package statics

import (
	"archive/zip"
	"bufio"
	"debug/buildinfo"
	_ "embed"
	"encoding/json"
	"fmt"
	"github.com/cloudreve/Cloudreve/v4/application/constants"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/cloudreve/Cloudreve/v4/pkg/util"
	"github.com/gin-contrib/static"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const StaticFolder = "statics"

//go:embed assets.zip
var zipContent string

type GinFS struct {
	FS     http.FileSystem
	zipped FS
}

type version struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Open opens a file
func (b *GinFS) Open(name string) (http.File, error) {
	return b.FS.Open(name)
}

// Exists reports whether the file exists
func (b *GinFS) Exists(prefix string, filepath string) bool {
	if _, err := b.FS.Open(filepath); err != nil {
		return false
	}
	return true
}

// NewServerStaticFS initializes the static resource files
func NewServerStaticFS(l logging.Logger, statics fs.FS, isPro bool) (static.ServeFileSystem, error) {
	var staticFS static.ServeFileSystem
	if util.Exists(util.DataPath(StaticFolder)) {
		l.Info("Folder with %q already exists, it will be used to serve static files.", util.DataPath(StaticFolder))
		staticFS = static.LocalFile(util.DataPath(StaticFolder), false)
	} else {
		// Initialize static resources
		embedFS, err := fs.Sub(statics, "assets/build")
		if err != nil {
			return nil, fmt.Errorf("failed to initialize static resources: %w", err)
		}

		zipped, _ := statics.(FS)
		staticFS = &GinFS{
			FS:     http.FS(embedFS),
			zipped: zipped,
		}
	}
	// Check the static resource version
	f, err := staticFS.Open("version.json")
	if err != nil {
		l.Warning("Missing version identifier file in static resources, please delete \"statics\" folder and rebuild it.")
		return staticFS, nil
	}

	b, err := io.ReadAll(f)
	if err != nil {
		l.Warning("Failed to read version identifier file in static resources, please delete \"statics\" folder and rebuild it.")
		return staticFS, nil
	}

	var v version
	if err := json.Unmarshal(b, &v); err != nil {
		l.Warning("Failed to parse version identifier file in static resources: %s", err)
		return staticFS, nil
	}

	staticName := "fylvo-web" // Fylvo: the name in frontend/package.json
	if isPro {
		staticName += "-pro"
	}

	if v.Name != staticName {
		l.Error("Static resource version mismatch, please delete \"statics\" folder and rebuild it.")
	}

	if v.Version != constants.BackendVersion {
		l.Error("Static resource version mismatch [Current %s, Desired: %s], please delete \"statics\" folder and rebuild it.", v.Version, constants.BackendVersion)
	}

	return staticFS, nil
}

func NewStaticFS(l logging.Logger) fs.FS {
	zipReader, err := zip.NewReader(strings.NewReader(zipContent), int64(len(zipContent)))
	if err != nil {
		l.Panic("Static resource is not a valid zip file: %s", err)
	}

	entries := make(map[string]*zip.File, len(zipReader.File))
	for _, zf := range zipReader.File {
		entries[zf.Name] = zf
	}

	var files []file
	modTime := getBuildTime()
	err = fs.WalkDir(zipReader, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("cannot walk into %q: %w", path, err)
		}

		if path == "." {
			return nil
		}

		f := file{modTime: modTime}
		if d.IsDir() {
			f.name = path + "/"
		} else {
			f.name = path
			zf := entries[path]
			if zf == nil {
				return fmt.Errorf("cannot find %q", path)
			}
			f.zip = zf
			// Fylvo: unpacked when first read, most files are sent as they are packed (Gzip).
			f.data = sync.OnceValue(func() string {
				rc, err := zf.Open()
				if err != nil {
					l.Error("Cannot open static file %q: %s", path, err)
					return ""
				}
				defer rc.Close()
				data, err := io.ReadAll(rc)
				if err != nil {
					l.Error("Cannot read static file %q: %s", path, err)
				}
				return string(data)
			})
		}
		files = append(files, f)
		return nil
	})
	if err != nil {
		l.Panic("Failed to initialize static resources: %s", err)
	}

	sort.Slice(files, func(i, j int) bool {
		fi, fj := files[i], files[j]
		di, ei, _ := split(fi.name)
		dj, ej, _ := split(fj.name)

		if di != dj {
			return di < dj
		}
		return ei < ej
	})

	var embedFS FS
	embedFS.files = &files
	return embedFS
}

// Eject extracts the built-in static resources
func Eject(l logging.Logger, statics fs.FS) error {
	// Initialize static resources
	embedFS, err := fs.Sub(statics, "assets/build")
	if err != nil {
		l.Panic("Failed to initialize static resources: %s", err)
	}

	var walk func(relPath string, d fs.DirEntry, err error) error
	walk = func(relPath string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("failed to read info of %q: %s, skipping...", relPath, err)
		}

		if !d.IsDir() {
			// Write the file
			dst := util.DataPath(filepath.Join(StaticFolder, relPath))
			out, err := util.CreatNestedFile(dst)
			defer out.Close()

			if err != nil {
				return fmt.Errorf("failed to create file %q: %s, skipping...", dst, err)
			}

			l.Info("Ejecting %q...", dst)
			obj, _ := embedFS.Open(relPath)
			if _, err := io.Copy(out, bufio.NewReader(obj)); err != nil {
				return fmt.Errorf("cannot write file %q: %s, skipping...", relPath, err)
			}
		}
		return nil
	}

	// util.Log().Info("start exporting built-in static resources...")
	err = fs.WalkDir(embedFS, ".", walk)
	if err != nil {
		return fmt.Errorf("failed to eject static resources: %w", err)
	}

	l.Info("Finish ejecting static resources.")
	return nil
}

func getBuildTime() (buildTime time.Time) {
	buildTime = time.Now()
	exe, err := os.Executable()
	if err != nil {
		return
	}
	info, err := buildinfo.ReadFile(exe)
	if err != nil {
		return
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.time" && s.Value != "" {
			if t, err := time.Parse(time.RFC3339, s.Value); err == nil {
				buildTime = t
			}
			break
		}
	}
	return
}
