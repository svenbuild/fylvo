package explorer

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/stretchr/testify/require"
)

// Fylvo: a Craft editor runs in the browser and makes the WOPI calls itself, on this server.

// withCraft sets up WordCraft as the browser office for docx.
func (e *connectedEnv) withCraft(t *testing.T, action string) {
	viewer := types.Viewer{
		ID: "office", Type: types.ViewerTypeWopi, DisplayName: "WordCraft", Exts: []string{"docx"},
		WopiActions: map[string]map[types.ViewerAction]string{
			"docx": {types.ViewerActionEdit: action, types.ViewerActionView: action},
		},
		Props: map[string]string{"craft": "wordcraft"},
	}
	raw, err := json.Marshal([]types.ViewerGroup{{Viewers: []types.Viewer{viewer}}})
	require.NoError(t, err)
	e.set(t, "file_viewers", string(raw))
}

func TestCraftConnectedSession(t *testing.T) {
	e := newConnectedEnv(t)
	e.withCraft(t, "/craft/wordcraft/")
	e.sim.AddFile("", "letter.docx", []byte("version one"))

	res, err := e.officeSession(t, "/letter.docx", types.ViewerActionEdit)
	require.NoError(t, err)
	session, token := res.Session.ID, res.Session.AccessToken
	// The browser gets the address of the calls on this server, nothing else.
	require.Equal(t, "/api/v4/fylvo/connected/wopi/"+session, res.WopiSrc)
	require.True(t, *res.Edit)

	rec, _ := e.office(ConnectedOfficeInfo, http.MethodGet, session, token, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var info WopiFileInfo
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &info))
	require.True(t, info.UserCanWrite)
	require.False(t, info.SupportsLocks, "a connected storage has no locks; the editor saves without")

	// The save goes over the version it read; locks are not offered.
	rec, _ = e.office(ConnectedOfficeGet, http.MethodGet, session, token, nil)
	require.Equal(t, "version one", rec.Body.String())
	rec, _ = e.office(ConnectedOfficePut, http.MethodPost, session, token, []byte("version two"))
	require.Equal(t, http.StatusOK, rec.Code)
	rec, _ = e.office(ConnectedOfficeOther, http.MethodPost, session, token, nil)
	require.Equal(t, http.StatusNotImplemented, rec.Code)
}

func TestCraftOnlyOnItsOwnRoute(t *testing.T) {
	e := newConnectedEnv(t)
	e.sim.AddFile("", "letter.docx", []byte("version one"))
	// A relative address elsewhere on this server is no editor.
	e.withCraft(t, "/api/v4/user/me")
	_, err := e.officeSession(t, "/letter.docx", types.ViewerActionEdit)
	require.Error(t, err)
}
