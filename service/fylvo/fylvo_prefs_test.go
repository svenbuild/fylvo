package fylvo

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/cloudreve/Cloudreve/v4/application/dependency"
	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/manager"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/stretchr/testify/require"
)

// Fylvo: account preferences, favorites and tags (design/customize-plan.md).

func (e *activityEnv) reload(u *ent.User) *ent.User {
	fresh, err := e.dep.UserClient().GetByID(context.WithValue(e.ctx, inventory.LoadUserGroup{}, true), u.ID)
	require.NoError(e.t, err)
	return fresh
}

func (e *activityEnv) patch(u *ent.User, body string) (*PrefsResponse, error) {
	var parts map[string]json.RawMessage
	require.NoError(e.t, json.Unmarshal([]byte(body), &parts))
	return PatchPrefs(e.as(e.reload(u)), parts)
}

func requireParamError(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err)
	var appErr serializer.AppError
	require.ErrorAs(t, err, &appErr)
	require.Equal(t, serializer.CodeParamErr, appErr.Code)
}

func sidebarTypes(items []types.FylvoSidebarItem) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		s := item.Type
		if item.Category != "" {
			s += ":" + item.Category
		}
		if item.Uri != "" {
			s += ":" + item.Uri
		}
		if item.Hidden {
			s += " (hidden)"
		}
		out = append(out, s)
	}
	return out
}

func TestPrefsDefaultsLookLikeBefore(t *testing.T) {
	e := newActivityEnv(t)
	_, err := e.dep.UserClient().UpdateSettings(e.ctx, e.owner.ID, func(s *types.UserSetting) error {
		s.Pined = []types.PinedFile{{Uri: "cloudreve://my/Docs", Name: "Docs"}}
		return nil
	})
	require.NoError(t, err)

	res, err := GetPrefs(e.as(e.reload(e.owner)))
	require.NoError(t, err)
	require.Equal(t, []string{"home (hidden)", "my_files", "favorites", "tags", "category:image", "category:video",
		"category:audio", "category:document", "shared_with_me", "trash", "link:cloudreve://my/Docs"},
		sidebarTypes(res.Sidebar.Items))
	require.Equal(t, "", res.StartPage)
	require.Equal(t, ViewPrefs{Density: "compact", Layout: "grid", SortBy: "created_at", SortDirection: "asc"}, res.View)
	require.Equal(t, "system", res.Appearance.Mode)
	require.Equal(t, "#7484F4", res.Appearance.Accent)
	require.JSONEq(t, "{}", string(res.Mobile))
	require.Empty(t, res.Favorites)
	require.Empty(t, res.Customized)
	require.Len(t, res.Home.Widgets, 5)
}

func TestPrefsPatchSavesOnlyTheGivenParts(t *testing.T) {
	e := newActivityEnv(t)

	_, err := e.patch(e.owner, `{"appearance":{"mode":"dark"}}`)
	require.NoError(t, err)
	res, err := e.patch(e.owner, `{"appearance":{"accent":"#2EC4B6"},"view":{"density":"comfortable"},"start_page":"home"}`)
	require.NoError(t, err)
	require.Equal(t, "dark", res.Appearance.Mode, "an accent change keeps the mode")
	require.Equal(t, "#2EC4B6", res.Appearance.Accent)
	require.Equal(t, "comfortable", res.View.Density)
	require.Equal(t, "home", res.StartPage)
	require.ElementsMatch(t, []string{"mode", "density", "start_page"}, res.Customized)
	require.Equal(t, "#2EC4B6", e.reload(e.owner).Settings.PreferredTheme, "the accent lives in preferred_theme")

	// Only the accent: no mode is stored.
	_, err = e.patch(e.member, `{"appearance":{"accent":"#A78BFA"}}`)
	require.NoError(t, err)
	member, err := GetPrefs(e.as(e.reload(e.member)))
	require.NoError(t, err)
	require.NotContains(t, member.Customized, "mode")

	// Mobile is stored as given.
	res, err = e.patch(e.owner, `{"mobile":{"tabs":["home","files"]}}`)
	require.NoError(t, err)
	require.JSONEq(t, `{"tabs":["home","files"]}`, string(res.Mobile))
	require.Equal(t, "dark", res.Appearance.Mode)
}

func TestPrefsPatchRefusesInvalidValues(t *testing.T) {
	e := newActivityEnv(t)
	for _, body := range []string{
		`{"appearance":{"mode":"purple"}}`,
		`{"appearance":{"accent":"#123456"}}`,
		`{"view":{"density":"huge"}}`,
		`{"view":{"layout":"list"}}`,
		`{"start_page":"trash"}`,
		`{"favorites":[]}`,
		`{"customized":[]}`,
		`{"unknown":1}`,
		`{"mobile":[1,2]}`,
		`{"sidebar":{"items":[{"type":"rocket"}]}}`,
		`{"sidebar":{"items":[{"type":"trash"},{"type":"trash"}]}}`,
		`{"sidebar":{"items":[{"type":"category","category":"books"}]}}`,
		`{"sidebar":{"items":[{"type":"heading"}]}}`,
		`{"sidebar":{"items":[{"type":"link","uri":"cloudreve://my/a"},{"type":"link","uri":"cloudreve://my/a"}]}}`,
		`{"home":{"widgets":[{"type":"weather"}]}}`,
		`{"sidebar":{"items":[{"type":"link","uri":"cloudreve://abc@connected/Docs"}]}}`,
		// One valid and one invalid part: nothing is saved.
		`{"start_page":"home","view":{"density":"huge"}}`,
	} {
		_, err := e.patch(e.owner, body)
		requireParamError(t, err)
	}

	big := `{"mobile":{"x":"` + strings.Repeat("a", mobileMaxBytes) + `"}}`
	var parts map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(big), &parts))
	_, err := PatchPrefs(e.as(e.reload(e.owner)), parts)
	requireParamError(t, err)

	res, err := GetPrefs(e.as(e.reload(e.owner)))
	require.NoError(t, err)
	require.Empty(t, res.Customized)
}

func TestPrefsSidebarKeepsPinsInStep(t *testing.T) {
	e := newActivityEnv(t)
	_, err := e.dep.UserClient().UpdateSettings(e.ctx, e.owner.ID, func(s *types.UserSetting) error {
		long := string(make([]rune, 100))
		s.Pined = []types.PinedFile{{Uri: "cloudreve://my/A", Name: "A"}, {Uri: "cloudreve://my/Legacy", Name: long}}
		return nil
	})
	require.NoError(t, err)
	before, err := GetPrefs(e.as(e.reload(e.owner)))
	require.NoError(t, err)

	// Reorder and hide, with the old pin that breaks the name limit: allowed.
	items := before.Sidebar.Items
	items[0], items[1] = items[1], items[0]
	items[2].Hidden = true
	items = append(items, types.FylvoSidebarItem{Type: SidebarLink, Uri: "cloudreve://my/B", Name: "B"},
		types.FylvoSidebarItem{Type: SidebarHeading, Name: "Work"})
	body, _ := json.Marshal(map[string]any{"sidebar": map[string]any{"items": items}})
	res, err := e.patch(e.owner, string(body))
	require.NoError(t, err)
	require.Contains(t, res.Customized, "sidebar")
	require.Equal(t, []types.PinedFile{{Uri: "cloudreve://my/A", Name: "A"}, e.reload(e.owner).Settings.Pined[1],
		{Uri: "cloudreve://my/B", Name: "B"}}, e.reload(e.owner).Settings.Pined, "links are written to pined in order")

	// The old pin calls change the sidebar too.
	_, err = e.dep.UserClient().UpdateSettings(e.ctx, e.owner.ID, func(s *types.UserSetting) error {
		s.Pined = []types.PinedFile{{Uri: "cloudreve://my/B", Name: "B"}, {Uri: "cloudreve://my/C"}}
		return nil
	})
	require.NoError(t, err)
	res, err = GetPrefs(e.as(e.reload(e.owner)))
	require.NoError(t, err)
	got := sidebarTypes(res.Sidebar.Items)
	require.NotContains(t, got, "link:cloudreve://my/A")
	require.Equal(t, "link:cloudreve://my/C", got[len(got)-1])
	require.Equal(t, "my_files", got[0])

	// Reset: the links stay, the rest is the default again.
	res, err = ResetPrefs(e.as(e.reload(e.owner)))
	require.NoError(t, err)
	require.Empty(t, res.Customized)
	got = sidebarTypes(res.Sidebar.Items)
	require.Equal(t, []string{"link:cloudreve://my/B", "link:cloudreve://my/C"}, got[len(got)-2:])
	require.Equal(t, "home (hidden)", got[0])
}

func TestPrefsSidebarAddsNewEntriesAndConnectedStorages(t *testing.T) {
	e := newActivityEnv(t)
	_, err := e.patch(e.owner, `{"sidebar":{"items":[{"type":"trash"},{"type":"my_files"},{"type":"connected","id":"gone"}]}}`)
	require.NoError(t, err)
	stored := e.reload(e.owner).Settings.FylvoPrefs.Sidebar
	require.Len(t, stored, 2, "an unknown connected storage is dropped")

	old := ConnectedStorages
	ConnectedStorages = func(context.Context, dependency.Dep, *ent.User) []string { return []string{"drive"} }
	defer func() { ConnectedStorages = old }()
	res, err := GetPrefs(e.as(e.reload(e.owner)))
	require.NoError(t, err)
	got := sidebarTypes(res.Sidebar.Items)
	require.Equal(t, "trash", got[0])
	require.Equal(t, "my_files", got[1])
	require.Contains(t, got, "favorites (hidden)", "fixed entries missing from a saved sidebar come hidden")
	require.Equal(t, "connected", got[len(got)-1], "a new connected storage shows at the end")
}

func TestPrefsViewIsTheRootView(t *testing.T) {
	e := newActivityEnv(t)
	fm := manager.NewFileManager(e.dep, e.owner)
	defer fm.Recycle()
	require.NoError(t, fm.PatchView(e.ctx, e.uri("cloudreve://my"), &types.ExplorerView{
		PageSize: 100, View: "list", Order: "name", OrderDirection: "desc", GalleryWidth: 220}))

	res, err := GetPrefs(e.as(e.reload(e.owner)))
	require.NoError(t, err)
	require.Equal(t, ViewPrefs{Density: "compact", Layout: "list", SortBy: "name", SortDirection: "desc"}, res.View)
	require.Contains(t, res.Customized, "view")

	_, err = e.dep.UserClient().UpdateSettings(e.ctx, e.owner.ID, func(s *types.UserSetting) error {
		s.DisableViewSync = true
		return nil
	})
	require.NoError(t, err)
	res, err = GetPrefs(e.as(e.reload(e.owner)))
	require.NoError(t, err)
	require.Equal(t, ViewPrefs{Density: "compact"}, res.View)
	require.NotContains(t, res.Customized, "view")
}

func TestSettingsWritersKeepEachOther(t *testing.T) {
	e := newActivityEnv(t)
	stale := e.reload(e.owner)

	_, err := e.patch(e.owner, `{"start_page":"home"}`)
	require.NoError(t, err)

	// The admin sets a new password with a copy loaded before the change.
	_, err = e.dep.UserClient().Upsert(e.ctx, stale, "admin-passwort", "", &types.UserSetting{FylvoMustChangePassword: true})
	require.NoError(t, err)
	fresh := e.reload(e.owner)
	require.True(t, fresh.Settings.FylvoMustChangePassword)
	require.Equal(t, "home", *fresh.Settings.FylvoPrefs.StartPage)

	// The person sets their own password with a copy loaded before another change.
	held := e.reload(e.owner)
	_, err = e.patch(e.owner, `{"view":{"density":"normal"}}`)
	require.NoError(t, err)
	require.NoError(t, e.dep.UserClient().SetOwnPassword(e.ctx, held, "eigenes-passwort"))
	fresh = e.reload(e.owner)
	require.False(t, fresh.Settings.FylvoMustChangePassword)
	require.Equal(t, "normal", fresh.Settings.FylvoPrefs.Density)

	// Many writers at once lose nothing.
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := e.dep.UserClient().UpdateSettings(e.ctx, e.owner.ID, func(s *types.UserSetting) error {
				s.Pined = append(s.Pined, types.PinedFile{Uri: "cloudreve://my/" + string(rune('a'+i))})
				return nil
			})
			require.NoError(t, err)
		}(i)
	}
	wg.Wait()
	require.Len(t, e.reload(e.owner).Settings.Pined, 20)
}

func favoriteNames(res *FavoritesResponse) []string {
	out := make([]string, 0, len(res.Files))
	for _, f := range res.Files {
		out = append(out, f.Path)
	}
	return out
}

func (e *activityEnv) star(u *ent.User, uris ...string) (*PrefsResponse, error) {
	return (&FavoritesAddService{Uris: uris}).Add(e.as(e.reload(u)))
}

func (e *activityEnv) favorites(u *ent.User) *FavoritesResponse {
	res, err := ListFavorites(e.as(e.reload(u)))
	require.NoError(e.t, err)
	return res
}

func TestFavoritesFollowTheirFiles(t *testing.T) {
	e := newActivityEnv(t)
	fm := manager.NewFileManager(e.dep, e.owner)
	defer fm.Recycle()
	_, err := fm.Create(e.ctx, e.uri("cloudreve://my/Private/a.txt"), types.FileTypeFile)
	require.NoError(t, err)

	res, err := e.star(e.owner, "cloudreve://my/Private/a.txt", "cloudreve://my/Shared")
	require.NoError(t, err)
	require.Len(t, res.Favorites, 2)
	require.Equal(t, []string{"cloudreve://my/Shared", "cloudreve://my/Private/a.txt"}, favoriteNames(e.favorites(e.owner)),
		"newest first")

	// Starring again changes nothing.
	res, err = e.star(e.owner, "cloudreve://my/Shared")
	require.NoError(t, err)
	require.Len(t, res.Favorites, 2)

	// Rename and move: the favorite follows.
	_, err = fm.Rename(e.ctx, e.uri("cloudreve://my/Private/a.txt"), "b.txt")
	require.NoError(t, err)
	require.NoError(t, fm.MoveOrCopy(e.ctx, []*fs.URI{e.uri("cloudreve://my/Private/b.txt")}, e.uri("cloudreve://my"), false))
	require.Contains(t, favoriteNames(e.favorites(e.owner)), "cloudreve://my/b.txt")

	// A folder above in the trash: unavailable, not listed.
	require.NoError(t, fm.Delete(e.ctx, []*fs.URI{e.uri("cloudreve://my/Shared")}))
	list := e.favorites(e.owner)
	require.Equal(t, []string{"cloudreve://my/b.txt"}, favoriteNames(list))
	require.Equal(t, 1, list.Unavailable)

	// Unstar by hash ID.
	res, err = (&FavoritesRemoveService{IDs: []string{res.Favorites[0], "nonsense"}}).Remove(e.as(e.reload(e.owner)))
	require.NoError(t, err)
	require.Len(t, res.Favorites, 1)
}

func TestFavoritesOfAMember(t *testing.T) {
	e := newActivityEnv(t)

	// Inside a shared folder and the shared folder itself.
	_, err := e.star(e.member, e.memberShare+"/doc.txt", e.memberShare)
	require.NoError(t, err)
	paths := favoriteNames(e.favorites(e.member))
	require.Len(t, paths, 2)
	require.Equal(t, e.memberShare+"/doc.txt", paths[1])

	// Not through a public link, not the owner's private files, not "shared with me" entries.
	// (Through the public link the member gets the same file it already has as member: allowed,
	// stored as the file, listed under the member address.)
	for _, uri := range []string{"cloudreve://my/Private", "cloudreve://shared_with_me/x",
		"cloudreve://my?name=doc", "cloudreve://my"} {
		_, err := e.star(e.member, uri)
		require.Error(t, err, uri)
	}
	_, err = e.star(e.outsider, e.memberShare+"/doc.txt")
	require.Error(t, err)
	_, err = e.star(e.outsider, e.link+"/doc.txt")
	require.Error(t, err, "a public link is not the outsider's")

	// Moved out of the shared folder: gone for the member, nothing leaks.
	owner := manager.NewFileManager(e.dep, e.owner)
	defer owner.Recycle()
	require.NoError(t, owner.MoveOrCopy(e.ctx, []*fs.URI{e.uri("cloudreve://my/Shared/doc.txt")}, e.uri("cloudreve://my/Private"), false))
	list := e.favorites(e.member)
	require.Len(t, list.Files, 1)
	require.Equal(t, 1, list.Unavailable)

	// The admin of the outsider kind gets no special path: the outsider sees nothing of it.
	_, err = e.dep.UserClient().UpdateSettings(e.ctx, e.outsider.ID, func(s *types.UserSetting) error {
		s.FylvoFavorites = []types.FylvoFavorite{{FileID: e.privateID}}
		return nil
	})
	require.NoError(t, err)
	list = e.favorites(e.outsider)
	require.Empty(t, list.Files)
	require.Equal(t, 1, list.Unavailable)
}

func TestTagsCountOwnLiveFiles(t *testing.T) {
	e := newActivityEnv(t)
	fm := manager.NewFileManager(e.dep, e.owner)
	defer fm.Recycle()
	_, err := fm.Create(e.ctx, e.uri("cloudreve://my/Private/x.txt"), types.FileTypeFile)
	require.NoError(t, err)
	_, err = fm.Create(e.ctx, e.uri("cloudreve://my/Old"), types.FileTypeFolder)
	require.NoError(t, err)
	_, err = fm.Create(e.ctx, e.uri("cloudreve://my/Old/y.txt"), types.FileTypeFile)
	require.NoError(t, err)

	tag := func(uri, name, color string) {
		require.NoError(t, fm.PatchMedata(e.ctx, []*fs.URI{e.uri(uri)}, fs.MetadataPatch{Key: "tag:" + name, Value: color}))
	}
	tag("cloudreve://my/Private/x.txt", "Important", "#e5484d")
	tag("cloudreve://my/Shared/doc.txt", "Important", "#e5484d")
	tag("cloudreve://my/Private", "Important", "")
	tag("cloudreve://my/Old/y.txt", "Archive", "")
	require.NoError(t, fm.Delete(e.ctx, []*fs.URI{e.uri("cloudreve://my/Old")}))

	res, err := ListTags(e.as(e.reload(e.owner)))
	require.NoError(t, err)
	require.Equal(t, []TagResponse{{Name: "Important", Color: "#e5484d", Count: 3}}, res,
		"a file inside a folder in the trash does not count")

	other, err := ListTags(e.as(e.reload(e.member)))
	require.NoError(t, err)
	require.Empty(t, other, "tags of the owner are not the member's")
}
