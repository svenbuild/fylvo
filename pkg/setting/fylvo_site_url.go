package setting

import (
	"os"
	"strings"
)

// EnvSiteURL is the address people use to open Fylvo, for example https://cloud.example.com. The
// office server needs the same value, so one variable sets both (see docker-compose.yml).
const EnvSiteURL = "FYLVO_URL"

// addSiteURLFromEnv sets the site address from FYLVO_URL, unless it is empty or CR_SETTING_siteURL
// already sets it. Empty means "not set": the address from the admin panel counts.
func addSiteURLFromEnv(settings map[string]any) bool {
	siteURL := strings.TrimRight(strings.TrimSpace(os.Getenv(EnvSiteURL)), "/")
	if siteURL == "" {
		return false
	}

	if _, set := settings["siteURL"]; set {
		return false
	}

	settings["siteURL"] = siteURL
	return true
}
