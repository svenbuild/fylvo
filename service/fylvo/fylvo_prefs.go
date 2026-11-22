package fylvo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"unicode/utf8"

	"github.com/cloudreve/Cloudreve/v4/application/dependency"
	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/cloudreve/Cloudreve/v4/pkg/hashid"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/gin-gonic/gin"
)

// Account preferences (design/customize-plan.md). The server knows the defaults and always
// answers with the effective values, so the web app and the phone app share one set of rules.
// Only what the person saved is stored (types.FylvoPrefs); without it everything looks as before.

const (
	sidebarMaxItems = 100
	nameMaxLength   = 64
	mobileMaxBytes  = 8 * 1024

	SidebarHome         = "home"
	SidebarMyFiles      = "my_files"
	SidebarFavorites    = "favorites"
	SidebarTags         = "tags"
	SidebarSharedWithMe = "shared_with_me"
	SidebarTrash        = "trash"
	SidebarCategory     = "category"
	SidebarLink         = "link"
	SidebarHeading      = "heading"
	SidebarConnected    = "connected"
)

var (
	sidebarCategories = []string{"image", "video", "audio", "document"}
	homeWidgets       = []string{"recent", "favorites", "links", "tags", "storage"}
	startPages        = []string{"", "home", "my_files"}
	densities         = []string{"compact", "normal", "comfortable"}
	modes             = []string{"system", "light", "dark"}
)

// ConnectedStorages returns the IDs of the connected storages of an account, in their order.
// Connected storages do not exist yet; their feature fills this in. The sidebar is reconciled
// with it on every read.
var ConnectedStorages = func(ctx context.Context, dep dependency.Dep, u *ent.User) []string {
	return nil
}

// connectedFileSystem is the file system of connected storages in addresses.
const connectedFileSystem = "connected"

// CheckConnectedLink checks a sidebar link into a connected storage; the normal file checks
// refuse such addresses on purpose. Connected storages come later and fill this in; until then
// these links are refused.
var CheckConnectedLink = func(ctx context.Context, dep dependency.Dep, u *ent.User, uri *fs.URI) error {
	return paramError("links into connected storages are not supported")
}

type (
	PrefsResponse struct {
		Sidebar    SidebarPrefs    `json:"sidebar"`
		Home       HomePrefs       `json:"home"`
		StartPage  string          `json:"start_page"`
		View       ViewPrefs       `json:"view"`
		Appearance AppearancePrefs `json:"appearance"`
		Mobile     json.RawMessage `json:"mobile"`
		Favorites  []string        `json:"favorites"`
		Customized []string        `json:"customized"`
	}

	SidebarPrefs struct {
		Items []types.FylvoSidebarItem `json:"items"`
	}

	HomePrefs struct {
		Widgets []types.FylvoHomeWidget `json:"widgets"`
	}

	ViewPrefs struct {
		Density       string `json:"density"`
		Layout        string `json:"layout,omitempty"`
		SortBy        string `json:"sort_by,omitempty"`
		SortDirection string `json:"sort_direction,omitempty"`
	}

	AppearancePrefs struct {
		Mode   string `json:"mode"`
		Accent string `json:"accent"`
	}
)

func paramError(format string, args ...any) error {
	return serializer.NewError(serializer.CodeParamErr, fmt.Sprintf(format, args...), nil)
}

// GetPrefs returns the effective preferences of the current account.
func GetPrefs(c *gin.Context) (*PrefsResponse, error) {
	u := inventory.UserFromContext(c)
	settings := u.Settings
	if settings == nil {
		settings = &types.UserSetting{}
	}

	return buildPrefs(c, dependency.FromContext(c), u, settings)
}

// PatchPrefs saves the given parts. Everything is checked first; nothing is saved when one part
// is invalid.
func PatchPrefs(c *gin.Context, body map[string]json.RawMessage) (*PrefsResponse, error) {
	dep := dependency.FromContext(c)
	u := inventory.UserFromContext(c)

	changes := make([]func(*types.UserSetting) error, 0, len(body))
	for key, raw := range body {
		change, err := prefsChange(c, dep, u, key, raw)
		if err != nil {
			return nil, err
		}
		changes = append(changes, change)
	}

	settings, err := dep.UserClient().UpdateSettings(c, u.ID, func(s *types.UserSetting) error {
		if s.FylvoPrefs == nil {
			s.FylvoPrefs = &types.FylvoPrefs{}
		}
		for _, change := range changes {
			if err := change(s); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, serializer.KeepAppError(err, serializer.CodeDBError, "Failed to save settings")
	}

	u.Settings = settings
	return buildPrefs(c, dep, u, settings)
}

// ResetPrefs drops all saved parts except the phone's own (mobile) and clears the accent color.
// Links stay, they are always kept in settings.pined; favorites and folder views stay as well.
func ResetPrefs(c *gin.Context) (*PrefsResponse, error) {
	dep := dependency.FromContext(c)
	u := inventory.UserFromContext(c)

	settings, err := dep.UserClient().UpdateSettings(c, u.ID, func(s *types.UserSetting) error {
		if s.FylvoPrefs != nil && len(s.FylvoPrefs.Mobile) > 0 {
			s.FylvoPrefs = &types.FylvoPrefs{Mobile: s.FylvoPrefs.Mobile}
		} else {
			s.FylvoPrefs = nil
		}
		s.PreferredTheme = ""
		return nil
	})
	if err != nil {
		return nil, serializer.KeepAppError(err, serializer.CodeDBError, "Failed to save settings")
	}

	u.Settings = settings
	return buildPrefs(c, dep, u, settings)
}

// prefsChange checks one top-level part of a PATCH and returns the change to apply.
func prefsChange(ctx context.Context, dep dependency.Dep, u *ent.User, key string, raw json.RawMessage) (func(*types.UserSetting) error, error) {
	switch key {
	case "sidebar":
		// null drops the saved arrangement (back to the default); the links stay in pined.
		if string(raw) == "null" {
			return func(s *types.UserSetting) error { s.FylvoPrefs.Sidebar = nil; return nil }, nil
		}
		var sidebar struct {
			Items *[]types.FylvoSidebarItem `json:"items"`
		}
		if err := strictUnmarshal(raw, &sidebar); err != nil || sidebar.Items == nil {
			return nil, paramError("sidebar needs a list of items")
		}
		for _, item := range *sidebar.Items {
			if item.Type != SidebarLink {
				continue
			}
			if uri, err := fs.NewUriFromString(item.Uri); err == nil && string(uri.FileSystem()) == connectedFileSystem {
				if err := CheckConnectedLink(ctx, dep, u, uri); err != nil {
					return nil, err
				}
			}
		}
		allowed := connectedSet(ConnectedStorages(ctx, dep, u))
		return func(s *types.UserSetting) error {
			previous := len(effectiveSidebar(s.FylvoPrefs.Sidebar, s.Pined, nil))
			items, err := checkSidebar(*sidebar.Items, s.Pined, allowed, previous)
			if err != nil {
				return err
			}
			s.FylvoPrefs.Sidebar = items
			s.Pined = linksOf(items)
			return nil
		}, nil

	case "home":
		if string(raw) == "null" {
			return func(s *types.UserSetting) error { s.FylvoPrefs.Home = nil; return nil }, nil
		}
		var home struct {
			Widgets *[]types.FylvoHomeWidget `json:"widgets"`
		}
		if err := strictUnmarshal(raw, &home); err != nil || home.Widgets == nil {
			return nil, paramError("home needs a list of widgets")
		}
		widgets, err := checkHome(*home.Widgets)
		if err != nil {
			return nil, err
		}
		return func(s *types.UserSetting) error { s.FylvoPrefs.Home = widgets; return nil }, nil

	case "start_page":
		var page string
		if err := json.Unmarshal(raw, &page); err != nil || !slices.Contains(startPages, page) {
			return nil, paramError("invalid start_page")
		}
		return func(s *types.UserSetting) error {
			s.FylvoPrefs.StartPage = nil
			if page != "" {
				s.FylvoPrefs.StartPage = &page
			}
			return nil
		}, nil

	case "view":
		var view struct {
			Density *string `json:"density"`
		}
		if err := strictUnmarshal(raw, &view); err != nil {
			return nil, paramError("only view.density can be changed here: %s", err)
		}
		if view.Density == nil {
			return func(s *types.UserSetting) error { return nil }, nil
		}
		if !slices.Contains(densities, *view.Density) {
			return nil, paramError("invalid view.density")
		}
		return func(s *types.UserSetting) error { s.FylvoPrefs.Density = *view.Density; return nil }, nil

	case "appearance":
		var appearance struct {
			Mode   *string `json:"mode"`
			Accent *string `json:"accent"`
		}
		if err := strictUnmarshal(raw, &appearance); err != nil {
			return nil, paramError("invalid appearance: %s", err)
		}
		if appearance.Mode != nil && !slices.Contains(modes, *appearance.Mode) {
			return nil, paramError("invalid appearance.mode")
		}
		if appearance.Accent != nil && *appearance.Accent != "" && !themeKeys(ctx, dep)[*appearance.Accent] {
			return nil, paramError("unknown appearance.accent")
		}
		return func(s *types.UserSetting) error {
			if appearance.Mode != nil {
				s.FylvoPrefs.Mode = *appearance.Mode
			}
			if appearance.Accent != nil {
				s.PreferredTheme = *appearance.Accent
			}
			return nil
		}, nil

	case "mobile":
		if string(raw) == "null" {
			return func(s *types.UserSetting) error { s.FylvoPrefs.Mobile = nil; return nil }, nil
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err != nil || object == nil {
			return nil, paramError("mobile must be an object")
		}
		compact, _ := json.Marshal(object)
		if len(compact) > mobileMaxBytes {
			return nil, paramError("mobile is larger than %d bytes", mobileMaxBytes)
		}
		return func(s *types.UserSetting) error { s.FylvoPrefs.Mobile = compact; return nil }, nil
	}

	return nil, paramError("unknown or read-only field %q", key)
}

// checkSidebar validates a sidebar sent by an app and normalizes its links. Old pins may break
// the limits (a long name, more entries than allowed), so they never block a change; a sidebar
// may only not grow beyond the limit or beyond what it had.
func checkSidebar(items []types.FylvoSidebarItem, pined []types.PinedFile, connected map[string]bool,
	previous int) ([]types.FylvoSidebarItem, error) {
	legacy := make(map[types.PinedFile]bool, len(pined))
	for _, p := range pined {
		legacy[p] = true
	}

	result := make([]types.FylvoSidebarItem, 0, len(items))
	seen := make(map[string]bool)
	for _, item := range items {
		key := item.Type
		switch item.Type {
		case SidebarHome, SidebarMyFiles, SidebarFavorites, SidebarTags, SidebarSharedWithMe, SidebarTrash:
			item = types.FylvoSidebarItem{Type: item.Type, Hidden: item.Hidden}
		case SidebarCategory:
			if !slices.Contains(sidebarCategories, item.Category) {
				return nil, paramError("unknown category %q", item.Category)
			}
			key += ":" + item.Category
			item = types.FylvoSidebarItem{Type: item.Type, Category: item.Category, Hidden: item.Hidden}
		case SidebarLink:
			uri, err := fs.NewUriFromString(item.Uri)
			if err != nil {
				return nil, paramError("invalid link %q", item.Uri)
			}
			item = types.FylvoSidebarItem{Type: item.Type, Uri: uri.String(), Name: item.Name}
			key += ":" + item.Uri
			if !legacy[types.PinedFile{Uri: item.Uri, Name: item.Name}] {
				if utf8.RuneCountInString(item.Name) > nameMaxLength {
					return nil, paramError("link name is longer than %d characters", nameMaxLength)
				}
			}
		case SidebarHeading:
			if item.Name == "" || utf8.RuneCountInString(item.Name) > nameMaxLength {
				return nil, paramError("a heading needs a name of 1 to %d characters", nameMaxLength)
			}
			item = types.FylvoSidebarItem{Type: item.Type, Name: item.Name}
			key = ""
		case SidebarConnected:
			if !connected[item.ID] {
				continue // a storage that is gone (or not there yet) is dropped
			}
			key += ":" + item.ID
			item = types.FylvoSidebarItem{Type: item.Type, ID: item.ID, Hidden: item.Hidden}
		default:
			return nil, paramError("unknown sidebar entry %q", item.Type)
		}

		if key != "" {
			if seen[key] {
				return nil, paramError("sidebar entry %q appears twice", key)
			}
			seen[key] = true
		}
		result = append(result, item)
	}

	if len(result) > max(sidebarMaxItems, previous) {
		return nil, paramError("the sidebar can have at most %d entries", sidebarMaxItems)
	}

	return result, nil
}

func checkHome(widgets []types.FylvoHomeWidget) ([]types.FylvoHomeWidget, error) {
	result := make([]types.FylvoHomeWidget, 0, len(widgets))
	seen := make(map[string]bool)
	for _, w := range widgets {
		if !slices.Contains(homeWidgets, w.Type) {
			return nil, paramError("unknown home widget %q", w.Type)
		}
		if seen[w.Type] {
			return nil, paramError("home widget %q appears twice", w.Type)
		}
		seen[w.Type] = true
		result = append(result, types.FylvoHomeWidget{Type: w.Type, Hidden: w.Hidden})
	}
	return result, nil
}

func linksOf(items []types.FylvoSidebarItem) []types.PinedFile {
	var links []types.PinedFile
	for _, item := range items {
		if item.Type == SidebarLink {
			links = append(links, types.PinedFile{Uri: item.Uri, Name: item.Name})
		}
	}
	return links
}

func connectedSet(ids []string) map[string]bool {
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return set
}

// defaultSidebar is the sidebar of an account that never changed it: as before, plus the new
// entries. Start is off; favorites and tags only show when there are any.
func defaultSidebar() []types.FylvoSidebarItem {
	items := []types.FylvoSidebarItem{
		{Type: SidebarHome, Hidden: true},
		{Type: SidebarMyFiles},
		{Type: SidebarFavorites},
		{Type: SidebarTags},
	}
	for _, category := range sidebarCategories {
		items = append(items, types.FylvoSidebarItem{Type: SidebarCategory, Category: category})
	}
	return append(items,
		types.FylvoSidebarItem{Type: SidebarSharedWithMe},
		types.FylvoSidebarItem{Type: SidebarTrash},
	)
}

// effectiveSidebar puts the saved sidebar together with what changed since: links follow
// settings.pined (the old pin calls change only that), connected storages follow the account,
// fixed entries a saved sidebar lacks come at the end, hidden.
func effectiveSidebar(saved []types.FylvoSidebarItem, pined []types.PinedFile, connected []string) []types.FylvoSidebarItem {
	if saved == nil {
		items := defaultSidebar()
		for _, p := range pined {
			items = append(items, types.FylvoSidebarItem{Type: SidebarLink, Uri: p.Uri, Name: p.Name})
		}
		for _, id := range connected {
			items = append(items, types.FylvoSidebarItem{Type: SidebarConnected, ID: id})
		}
		return items
	}

	pinnedNames := make(map[string]string, len(pined))
	for _, p := range pined {
		pinnedNames[p.Uri] = p.Name
	}
	connectedNow := connectedSet(connected)

	items := make([]types.FylvoSidebarItem, 0, len(saved)+len(pined))
	present := make(map[string]bool)
	for _, item := range saved {
		switch item.Type {
		case SidebarLink:
			name, ok := pinnedNames[item.Uri]
			if !ok || present["link:"+item.Uri] {
				continue
			}
			item.Name = name
			present["link:"+item.Uri] = true
		case SidebarConnected:
			if !connectedNow[item.ID] {
				continue
			}
			present["connected:"+item.ID] = true
		case SidebarCategory:
			present["category:"+item.Category] = true
		default:
			present[item.Type] = true
		}
		items = append(items, item)
	}

	for _, item := range defaultSidebar() {
		key := item.Type
		if item.Type == SidebarCategory {
			key += ":" + item.Category
		}
		if !present[key] {
			item.Hidden = true
			items = append(items, item)
		}
	}
	for _, p := range pined {
		if !present["link:"+p.Uri] {
			present["link:"+p.Uri] = true
			items = append(items, types.FylvoSidebarItem{Type: SidebarLink, Uri: p.Uri, Name: p.Name})
		}
	}
	for _, id := range connected {
		if !present["connected:"+id] {
			items = append(items, types.FylvoSidebarItem{Type: SidebarConnected, ID: id})
		}
	}

	return items
}

func effectiveHome(saved []types.FylvoHomeWidget) []types.FylvoHomeWidget {
	present := make(map[string]bool)
	widgets := make([]types.FylvoHomeWidget, 0, len(homeWidgets))
	for _, w := range saved {
		present[w.Type] = true
		widgets = append(widgets, w)
	}
	for _, t := range homeWidgets {
		if !present[t] {
			// A widget missing from a saved list is new since; it comes hidden.
			widgets = append(widgets, types.FylvoHomeWidget{Type: t, Hidden: saved != nil})
		}
	}
	return widgets
}

// themeKeys returns the accent colors the site offers.
func themeKeys(ctx context.Context, dep dependency.Dep) map[string]bool {
	var themes map[string]json.RawMessage
	_ = json.Unmarshal([]byte(dep.SettingProvider().Theme(ctx).Themes), &themes)
	keys := make(map[string]bool, len(themes))
	for key := range themes {
		keys[key] = true
	}
	return keys
}

func buildPrefs(ctx context.Context, dep dependency.Dep, u *ent.User, s *types.UserSetting) (*PrefsResponse, error) {
	prefs := s.FylvoPrefs
	if prefs == nil {
		prefs = &types.FylvoPrefs{}
	}

	res := &PrefsResponse{
		Sidebar:    SidebarPrefs{Items: effectiveSidebar(prefs.Sidebar, s.Pined, ConnectedStorages(ctx, dep, u))},
		Home:       HomePrefs{Widgets: effectiveHome(prefs.Home)},
		View:       ViewPrefs{Density: "compact"},
		Appearance: AppearancePrefs{Mode: "system"},
		Mobile:     json.RawMessage("{}"),
		Customized: []string{},
	}

	if prefs.Sidebar != nil {
		res.Customized = append(res.Customized, "sidebar")
	}
	if prefs.Home != nil {
		res.Customized = append(res.Customized, "home")
	}
	if prefs.StartPage != nil {
		res.StartPage = *prefs.StartPage
		res.Customized = append(res.Customized, "start_page")
	}
	if prefs.Density != "" {
		res.View.Density = prefs.Density
		res.Customized = append(res.Customized, "density")
	}
	if prefs.Mode != "" {
		res.Appearance.Mode = prefs.Mode
		res.Customized = append(res.Customized, "mode")
	}
	if len(prefs.Mobile) > 0 {
		res.Mobile = prefs.Mobile
		res.Customized = append(res.Customized, "mobile")
	}

	theme := dep.SettingProvider().Theme(ctx)
	res.Appearance.Accent = theme.DefaultTheme
	if themeKeys(ctx, dep)[s.PreferredTheme] {
		res.Appearance.Accent = s.PreferredTheme
	}

	// The default view is the view of the "My files" root folder (written with PATCH /file/view).
	if !s.DisableViewSync {
		res.View.Layout, res.View.SortBy, res.View.SortDirection = "grid", "created_at", "asc"
		// An account that never opened its files has no root folder yet.
		root, err := dep.FileClient().Root(ctx, u)
		if err != nil && !ent.IsNotFound(err) {
			return nil, serializer.NewError(serializer.CodeDBError, "Failed to read the root folder", err)
		}
		if root != nil && root.Props != nil && root.Props.View != nil {
			view := root.Props.View
			if view.View != "" {
				res.View.Layout = view.View
			}
			if view.Order != "" {
				res.View.SortBy = view.Order
			}
			if view.OrderDirection != "" {
				res.View.SortDirection = view.OrderDirection
			}
			res.Customized = append(res.Customized, "view")
		}
	}

	res.Favorites = favoriteIDs(dep, s)
	return res, nil
}

// favoriteIDs returns the hash IDs of the favorites, newest first.
func favoriteIDs(dep dependency.Dep, s *types.UserSetting) []string {
	ids := make([]string, 0, len(s.FylvoFavorites))
	for i := len(s.FylvoFavorites) - 1; i >= 0; i-- {
		ids = append(ids, hashid.EncodeFileID(dep.HashIDEncoder(), s.FylvoFavorites[i].FileID))
	}
	return ids
}

func strictUnmarshal(raw json.RawMessage, v any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(v)
}
