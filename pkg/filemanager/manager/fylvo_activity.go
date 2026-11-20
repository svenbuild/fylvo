package manager

import (
	"context"
	"strconv"
	"time"

	"github.com/cloudreve/Cloudreve/v4/application/dependency"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/pkg/crontab"
	"github.com/cloudreve/Cloudreve/v4/pkg/setting"
)

// activityDeleteChunk is how many old activity log entries are removed per statement, so that
// the single SQLite connection is never held for long.
const activityDeleteChunk = 1000

func init() {
	crontab.Register(setting.CronTypeFylvoActivityRetention, CronRemoveOldActivity)
}

// ActivityRetentionDays returns how many days activity log entries are kept (Fylvo). A missing
// or broken setting means the default; 0 or less keeps everything.
func ActivityRetentionDays(ctx context.Context, dep dependency.Dep) int {
	raw, err := dep.SettingClient().Get(ctx, inventory.ActivityRetentionSetting)
	if err != nil {
		raw = inventory.ActivityRetentionDefault
	}

	days, err := strconv.Atoi(raw)
	if err != nil {
		days, _ = strconv.Atoi(inventory.ActivityRetentionDefault)
	}
	return days
}

// CronRemoveOldActivity removes activity log entries older than the retention.
func CronRemoveOldActivity(ctx context.Context) {
	dep := dependency.FromContext(ctx)
	removeOldActivity(ctx, dep, inventory.NewActivityClient(dep.DBClient()), time.Now())
}

func removeOldActivity(ctx context.Context, dep dependency.Dep, client inventory.ActivityClient, now time.Time) int {
	days := ActivityRetentionDays(ctx, dep)
	if days <= 0 {
		return 0
	}

	before := now.AddDate(0, 0, -days)
	total := 0
	for {
		n, err := client.DeleteBefore(ctx, before, activityDeleteChunk)
		if err != nil {
			dep.Logger().Warning("Failed to remove old activity log entries: %s", err)
			return total
		}

		total += n
		if n < activityDeleteChunk || ctx.Err() != nil {
			break
		}
	}

	if total > 0 {
		dep.Logger().Info("Removed %d activity log entries older than %d days.", total, days)
	}
	return total
}
