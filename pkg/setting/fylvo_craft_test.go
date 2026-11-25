package setting

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseOfficeEngines(t *testing.T) {
	// Without a list: Collabora first when set up, so its installations keep their default.
	assert.Equal(t, []string{"collabora", "craft"}, parseOfficeEngines("", true, true))
	assert.Equal(t, []string{"craft"}, parseOfficeEngines("", false, true))
	assert.Equal(t, []string{"collabora"}, parseOfficeEngines("", true, false))
	assert.Empty(t, parseOfficeEngines("", false, false))

	assert.Equal(t, []string{"craft", "collabora"}, parseOfficeEngines(" Craft , collabora ", true, true))
	// An engine left out is off, one that is not there is skipped, repeats count once.
	assert.Equal(t, []string{"collabora"}, parseOfficeEngines("collabora", true, true))
	assert.Equal(t, []string{"collabora"}, parseOfficeEngines("craft,collabora,collabora", true, false))
	assert.Empty(t, parseOfficeEngines("onlyoffice", true, true))
}

func TestInstalledCraftApps(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "wordcraft"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "wordcraft", "index.html"), nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "wordcraft", "wordcraft-0123456789abcdef.ico"), nil, 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "gridcraft"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "gridcraft", "index.html"), nil, 0o644))
	// A folder without its page is not an installed app.
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "deckcraft"), 0o755))

	apps := installedCraftApps(dir)
	assert.Equal(t, map[string]string{"wordcraft": "/craft/wordcraft/wordcraft-0123456789abcdef.ico", "gridcraft": ""}, apps)

	group := craftViewers(apps)
	require.Len(t, group.Viewers, 2)
	word := group.Viewers[0]
	assert.Equal(t, "craft-word", word.ID)
	assert.True(t, IsCraftViewer(&word))
	assert.Equal(t, "pc", word.Platform, "not on phones")
	assert.Equal(t, types.ViewerType(types.ViewerTypeWopi), word.Type)
	assert.Equal(t, []string{"docx"}, word.Exts)
	assert.Equal(t, "/craft/wordcraft/", word.WopiActions["docx"][types.ViewerActionEdit])
	assert.Equal(t, "/craft/wordcraft/", word.WopiActions["docx"][types.ViewerActionView])
	assert.Equal(t, []types.NewFileTemplate{{Ext: "docx", DisplayName: "fileManager.officeDocument"}}, word.Templates)
	assert.Empty(t, installedCraftApps(filepath.Join(dir, "missing")))
}

// withEngines sets what the environment would have set at start.
func withEngines(t *testing.T, engines []string, icons map[string]string) {
	loadCraft()
	oldEngines, oldIcons, oldURL := officeEngines, craftIcons, officeURL
	t.Cleanup(func() { officeEngines, craftIcons, officeURL = oldEngines, oldIcons, oldURL })
	officeEngines, craftIcons, officeURL = engines, icons, "https://office.example.com"
}

func TestWithOfficeViewers(t *testing.T) {
	all := map[string]string{"wordcraft": "", "gridcraft": "", "deckcraft": "", "pdfcraft": ""}
	groups := []types.ViewerGroup{{}}

	withEngines(t, nil, all)
	assert.Len(t, withOfficeViewers(groups), 1)

	// Only the default engine offers "New".
	withEngines(t, []string{"craft", "collabora"}, all)
	got := withOfficeViewers(groups)
	require.Len(t, got, 3)
	assert.Equal(t, "craft-word", got[1].Viewers[0].ID)
	assert.NotEmpty(t, got[1].Viewers[0].Templates)
	for _, viewer := range got[2].Viewers {
		assert.Empty(t, viewer.Templates, viewer.ID)
	}

	withEngines(t, []string{"collabora", "craft"}, all)
	got = withOfficeViewers(groups)
	require.Len(t, got, 3)
	assert.Equal(t, "office-writer", got[1].Viewers[0].ID)
	assert.NotEmpty(t, got[1].Viewers[0].Templates)
	for _, viewer := range got[2].Viewers {
		assert.Empty(t, viewer.Templates, viewer.ID)
	}
}

func TestWithOfficeDefaults(t *testing.T) {
	all := map[string]string{"wordcraft": "", "gridcraft": "", "deckcraft": "", "pdfcraft": ""}

	withEngines(t, []string{"craft", "collabora"}, all)
	got := withOfficeDefaults(types.DefaultViewerMapping{"xlsx": "office-calc"})
	assert.Equal(t, "craft-word", got["docx"])
	assert.Equal(t, "craft-deck", got["pptx"])
	// The admin's choice stays; PDF has no second office engine and keeps what it had.
	assert.Equal(t, "office-calc", got["xlsx"])
	assert.Empty(t, got["pdf"])

	// Only formats both engines take: the others open as before.
	withEngines(t, []string{"collabora", "craft"}, all)
	got = withOfficeDefaults(nil)
	assert.Equal(t, types.DefaultViewerMapping{"docx": "office-writer", "xlsx": "office-calc", "pptx": "office-impress"}, got)

	// One engine: nothing to choose between, the mapping stays as it was.
	withEngines(t, []string{"collabora"}, all)
	assert.Empty(t, withOfficeDefaults(types.DefaultViewerMapping{}))
}

func TestCraftDirOnlyInUse(t *testing.T) {
	withEngines(t, []string{"collabora"}, map[string]string{"wordcraft": ""})
	assert.Empty(t, CraftDir())
	withEngines(t, []string{"craft"}, map[string]string{"wordcraft": ""})
	assert.NotEmpty(t, CraftDir())
}
