package setting

import (
	"strings"
	"testing"

	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCleanOfficeURL(t *testing.T) {
	assert.Equal(t, "https://office.example.com", cleanOfficeURL(" https://office.example.com/ "))
	assert.Equal(t, "http://192.168.1.2:9980", cleanOfficeURL("http://192.168.1.2:9980"))
	assert.Equal(t, "", cleanOfficeURL(""))
	assert.Equal(t, "", cleanOfficeURL("office.example.com"))
	assert.Equal(t, "", cleanOfficeURL("http://:9980"))
	assert.Equal(t, "", cleanOfficeURL("javascript:alert(1)"))
	assert.Equal(t, "", cleanOfficeURL("https://user:secret@office.example.com"))
	assert.Equal(t, "", cleanOfficeURL("https://office.example.com/?next=x"))
	assert.Equal(t, "", cleanOfficeURL("https://office.example.com/#x"))
	assert.Equal(t, "", cleanOfficeURL("https://office.example.com?"))
	assert.Equal(t, "", cleanOfficeURL("https://office.example.com/#"))
}

func TestOfficeViewers(t *testing.T) {
	const base = "https://office.example.com"
	group, err := officeViewers(base)
	require.NoError(t, err)
	require.Len(t, group.Viewers, 4)

	seenExts := map[string]string{}
	for _, viewer := range group.Viewers {
		assert.Equal(t, types.ViewerType(types.ViewerTypeWopi), viewer.Type)
		assert.Len(t, viewer.WopiActions, len(viewer.Exts), viewer.ID)
		if viewer.Icon != "" {
			assert.True(t, strings.HasPrefix(viewer.Icon, base+"/"), viewer.Icon)
		}

		for _, ext := range viewer.Exts {
			assert.Empty(t, seenExts[ext], "extension %q is claimed twice", ext)
			seenExts[ext] = viewer.ID

			actions := viewer.WopiActions[ext]
			require.NotEmpty(t, actions, "%s has no action for %q", viewer.ID, ext)
			for action, src := range actions {
				assert.True(t, strings.HasPrefix(src, base+"/"), src)
				// Only the view address asks Collabora for read-only mode.
				assert.Equal(t, action == types.ViewerActionView, strings.Contains(src, "permission=readonly"), src)
			}
		}

		for _, template := range viewer.Templates {
			assert.Contains(t, viewer.WopiActions[template.Ext], types.ViewerAction(types.ViewerActionEdit))
		}
	}

	// Formats the built-in viewers handle better stay with them.
	for _, ext := range []string{"txt", "md", "png", "jpg", "pdf", "svg"} {
		assert.Empty(t, seenExts[ext], "office must not claim %q", ext)
	}

	// The result is a copy: changing it must not leak into the next call.
	group.Viewers[0].WopiActions = nil
	again, err := officeViewers(base)
	require.NoError(t, err)
	assert.NotEmpty(t, again.Viewers[0].WopiActions)
}
