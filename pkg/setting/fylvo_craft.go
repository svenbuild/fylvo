package setting

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/util"
)

// Fylvo: office editing with the Craft apps (WordCraft, GridCraft, DeckCraft, PdfCraft). They
// run in the browser (WebAssembly) and are served by Fylvo itself from FYLVO_CRAFT_DIR, where
// scripts/craft/fetch.sh puts releases checked against craft.lock. The browser opens and saves
// the files through the same WOPI calls the Collabora server makes, so locks, versions, rights
// and connected storages work the same for both.

const (
	EnvCraftDir     = "FYLVO_CRAFT_DIR"
	EnvOfficeEngine = "FYLVO_OFFICE_ENGINE"

	// CraftRoute is where the browser loads the editors from: CraftRoute + "<app>/".
	CraftRoute = "/craft/"
	// CraftProp names the Craft app of a viewer in its props.
	CraftProp = "craft"

	engineCraft     = "craft"
	engineCollabora = "collabora"
)

type craftApp struct {
	id   string
	app  string
	name string
	ext  string
	// template is the name of the "New" entry, "" for none.
	template string
}

// Only formats the apps save in the format they opened: GridCraft saves a CSV as .xlsx, for
// example, so CSV stays with the other editors.
var craftApps = []craftApp{
	{id: "craft-word", app: "wordcraft", name: "WordCraft", ext: "docx", template: "fileManager.officeDocument"},
	{id: "craft-grid", app: "gridcraft", name: "GridCraft", ext: "xlsx", template: "fileManager.officeSpreadsheet"},
	{id: "craft-deck", app: "deckcraft", name: "DeckCraft", ext: "pptx", template: "fileManager.officePresentation"},
	{id: "craft-pdf", app: "pdfcraft", name: "PdfCraft", ext: "pdf"},
}

var (
	craftOnce sync.Once
	craftPath string
	// craftIcons holds the icon of every installed app by its name, "" for an app without one.
	craftIcons map[string]string
	// officeEngines are the engines in use, the default first.
	officeEngines []string
)

// loadCraft looks for the editors once, after the command line was read (a relative folder
// follows --use-working-dir).
func loadCraft() {
	craftOnce.Do(func() {
		craftPath = craftDir(os.Getenv(EnvCraftDir))
		craftIcons = installedCraftApps(craftPath)
		officeEngines = parseOfficeEngines(os.Getenv(EnvOfficeEngine), officeURL != "", len(craftIcons) > 0)
	})
}

// CraftDir is the folder the editors are served from, "" when Craft is not in use.
func CraftDir() string {
	loadCraft()
	if !slices.Contains(officeEngines, engineCraft) {
		return ""
	}
	return craftPath
}

func craftDir(dir string) string {
	if dir == "" {
		dir = "craft"
	}
	return util.RelativePath(dir)
}

// installedCraftApps finds the apps that are in dir. The icon is the app's own (its name
// carries a hash, so it is looked up).
func installedCraftApps(dir string) map[string]string {
	apps := map[string]string{}
	for _, a := range craftApps {
		if !util.Exists(filepath.Join(dir, a.app, "index.html")) {
			continue
		}
		icon := ""
		if found, _ := filepath.Glob(filepath.Join(dir, a.app, a.app+"-*.ico")); len(found) > 0 {
			icon = CraftRoute + a.app + "/" + filepath.Base(found[0])
		}
		apps[a.app] = icon
	}
	return apps
}

// parseOfficeEngines returns the office engines in use, the default one first. raw lists them
// in that order ("craft,collabora"); an engine it leaves out is off. Without a list Collabora
// comes first when it is set up, so installations from before keep their default.
func parseOfficeEngines(raw string, collabora, craft bool) []string {
	available := map[string]bool{engineCollabora: collabora, engineCraft: craft}
	if strings.TrimSpace(raw) == "" {
		raw = engineCollabora + "," + engineCraft
	}
	var engines []string
	for _, engine := range strings.Split(raw, ",") {
		engine = strings.ToLower(strings.TrimSpace(engine))
		if available[engine] {
			engines = append(engines, engine)
			available[engine] = false
		}
	}
	return engines
}

// craftViewers builds the viewer group of the installed apps. Both actions lead to the same
// page; what a session may do is told by CheckFileInfo, and the server refuses a save of a
// session that may only read.
func craftViewers(icons map[string]string) *types.ViewerGroup {
	group := &types.ViewerGroup{}
	for _, a := range craftApps {
		icon, ok := icons[a.app]
		if !ok {
			continue
		}
		src := CraftRoute + a.app + "/"
		viewer := types.Viewer{
			ID:          a.id,
			Type:        types.ViewerTypeWopi,
			DisplayName: a.name,
			Exts:        []string{a.ext},
			Icon:        icon,
			WopiActions: map[string]map[types.ViewerAction]string{
				a.ext: {types.ViewerActionEdit: src, types.ViewerActionView: src},
			},
			Props: map[string]string{CraftProp: a.app},
		}
		if a.template != "" {
			viewer.Templates = []types.NewFileTemplate{{Ext: a.ext, DisplayName: a.template}}
		}
		group.Viewers = append(group.Viewers, viewer)
	}
	return group
}

// IsCraftViewer tells whether a viewer is one of the Craft apps.
func IsCraftViewer(viewer *types.Viewer) bool {
	return viewer.Props[CraftProp] != ""
}

// engineViewers returns a fresh copy of the viewer group of an engine.
func engineViewers(engine string) *types.ViewerGroup {
	if engine == engineCraft {
		return craftViewers(craftIcons)
	}
	group, err := officeViewers(officeURL)
	if err != nil {
		return nil
	}
	return group
}

// withOfficeViewers appends the viewers of the office engines in use. Only the default engine
// offers "New", so every kind of document is listed there once.
func withOfficeViewers(groups []types.ViewerGroup) []types.ViewerGroup {
	loadCraft()
	for i, engine := range officeEngines {
		group := engineViewers(engine)
		if group == nil {
			continue
		}
		if i > 0 {
			for j := range group.Viewers {
				group.Viewers[j].Templates = nil
			}
		}
		groups = append(groups, *group)
	}
	return groups
}

// withOfficeDefaults makes the default engine the default viewer of the formats both engines
// take, where the admin chose none, so such a file opens without asking. Other formats keep
// what they had.
func withOfficeDefaults(mapping types.DefaultViewerMapping) types.DefaultViewerMapping {
	loadCraft()
	if len(officeEngines) < 2 {
		return mapping
	}
	others := map[string]bool{}
	for _, engine := range officeEngines[1:] {
		if group := engineViewers(engine); group != nil {
			for _, viewer := range group.Viewers {
				for _, ext := range viewer.Exts {
					others[ext] = true
				}
			}
		}
	}
	if mapping == nil {
		mapping = types.DefaultViewerMapping{}
	}
	if group := engineViewers(officeEngines[0]); group != nil {
		for _, viewer := range group.Viewers {
			for _, ext := range viewer.Exts {
				if others[ext] && mapping[ext] == "" {
					mapping[ext] = viewer.ID
				}
			}
		}
	}
	return mapping
}
