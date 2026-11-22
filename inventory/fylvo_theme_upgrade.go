package inventory

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/ent/setting"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
)

// fylvoOldThemes lists the color themes earlier Fylvo versions stored as the default (the first
// navy draft and the single-accent theme).
//
//go:embed fylvo_theme_old.json
var fylvoOldThemes string

// upgradeThemes gives an instance that still has an earlier default theme list the current one,
// with all accent colors. A list the administrator changed stays. Runs on every start, because
// the schema migration only runs when the version changes.
func upgradeThemes(l logging.Logger, client *ent.Client, ctx context.Context) error {
	stored, err := client.Setting.Query().Where(setting.Name("theme_options")).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to read theme_options: %w", err)
	}

	var current any
	if err := json.Unmarshal([]byte(stored.Value), &current); err != nil {
		return nil // not ours to repair
	}

	var old []any
	if err := json.Unmarshal([]byte(fylvoOldThemes), &old); err != nil {
		return fmt.Errorf("failed to parse old themes: %w", err)
	}
	for _, o := range old {
		if reflect.DeepEqual(current, o) {
			if err := client.Setting.UpdateOne(stored).SetValue(fylvoTheme).Exec(ctx); err != nil {
				return fmt.Errorf("failed to update theme_options: %w", err)
			}
			l.Info("Updated the color themes to the current Fylvo default.")
			return nil
		}
	}

	return nil
}
