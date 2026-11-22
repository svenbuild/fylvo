package setting

import (
	_ "embed"
	"encoding/json"
	"net/url"
	"os"
	"strings"

	"github.com/cloudreve/Cloudreve/v4/inventory/types"
)

// Fylvo: office editing in the browser through a Collabora server (see docker-compose.yml).
//
// EnvOfficeURL holds the address under which browsers reach Collabora, for example
// https://office.example.com. When it is set, the office viewers are added to the viewers
// from the settings. They are not listed in the admin panel, the variable is the switch.
const EnvOfficeURL = "FYLVO_OFFICE_URL"

const officePlaceholder = "{office}"

//go:embed fylvo_office_viewers.json
var officeViewersTemplate string

var officeURL = cleanOfficeURL(os.Getenv(EnvOfficeURL))

// cleanOfficeURL returns the address without a trailing slash, or "" if it is not a plain web address.
// Login data, a query or a fragment have no place in it: access tokens are posted to this address.
func cleanOfficeURL(raw string) string {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	if strings.ContainsAny(raw, "?#") {
		return ""
	}

	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return ""
	}

	return parsed.String()
}

// officeViewers builds the viewer group for the Collabora server at base.
// Each call returns a fresh copy, callers are free to change it.
func officeViewers(base string) (*types.ViewerGroup, error) {
	var group types.ViewerGroup
	if err := json.Unmarshal([]byte(officeViewersTemplate), &group); err != nil {
		return nil, err
	}

	for i := range group.Viewers {
		viewer := &group.Viewers[i]
		viewer.Icon = strings.Replace(viewer.Icon, officePlaceholder, base, 1)
		for _, actions := range viewer.WopiActions {
			for action, src := range actions {
				actions[action] = strings.Replace(src, officePlaceholder, base, 1)
			}
		}
	}

	return &group, nil
}

// withOfficeViewers appends the office viewers if an office server is configured.
func withOfficeViewers(groups []types.ViewerGroup) []types.ViewerGroup {
	if officeURL == "" {
		return groups
	}

	office, err := officeViewers(officeURL)
	if err != nil {
		return groups
	}

	return append(groups, *office)
}
