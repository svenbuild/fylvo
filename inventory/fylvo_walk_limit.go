package inventory

import (
	"context"
	"fmt"

	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
)

// Fylvo: folder size, copy, ZIP download and WebDAV listings walk a folder up to the
// MaxWalkedFiles of the group. The walk now passes entries page by page and only holds the
// folders of a level, so its memory no longer grows with the number of entries. The default is
// raised so that a folder with all the files of a Nextcloud (about 174'000) is walked in full.
const (
	DefaultMaxWalkedFiles = 1000000
	// upstreamMaxWalkedFiles is the default of Cloudreve, which earlier Fylvo versions stored.
	upstreamMaxWalkedFiles = 100000
)

// raiseWalkLimit gives groups that still have the old default the new one. Runs on every start,
// because the schema migration only runs when the version changes. A value the administrator
// chose stays.
func raiseWalkLimit(l logging.Logger, client *ent.Client, ctx context.Context) error {
	groups, err := client.Group.Query().All(ctx)
	if err != nil {
		return fmt.Errorf("failed to list groups: %w", err)
	}
	for _, g := range groups {
		if g.Settings == nil || g.Settings.MaxWalkedFiles != upstreamMaxWalkedFiles {
			continue
		}
		settings := *g.Settings
		settings.MaxWalkedFiles = DefaultMaxWalkedFiles
		if err := client.Group.UpdateOneID(g.ID).SetSettings(&settings).Exec(ctx); err != nil {
			return fmt.Errorf("failed to raise the walk limit of group %d: %w", g.ID, err)
		}
		l.Info("Raised the walk limit of group %q to %d entries.", g.Name, DefaultMaxWalkedFiles)
	}
	return nil
}
