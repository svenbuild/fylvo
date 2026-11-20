package inventory

import (
	"context"
	"fmt"
	"time"

	"entgo.io/ent/dialect/sql"
	entsqlschema "entgo.io/ent/dialect/sql/schema"
	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/ent/activity"
	entmigrate "github.com/cloudreve/Cloudreve/v4/ent/migrate"
	"github.com/cloudreve/Cloudreve/v4/ent/predicate"
	"github.com/cloudreve/Cloudreve/v4/ent/schema"
	"github.com/cloudreve/Cloudreve/v4/ent/setting"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
)

// Fylvo activity log, see pkg/activity.

const (
	// ActivityRetentionSetting is the setting with the number of days entries are kept.
	ActivityRetentionSetting = "activity_retention_days"
	// ActivityRetentionDefault is used for new and existing installations.
	ActivityRetentionDefault = "180"

	activityPageSizeMax = 100
)

type (
	ActivityClient interface {
		TxOperator
		// CreateBulk stores the given entries in one transaction.
		CreateBulk(ctx context.Context, entries []*ent.ActivityCreate) error
		// List returns entries matching args, newest first.
		List(ctx context.Context, args *ListActivityArgs) (*ListActivityResult, error)
		// DeleteBefore removes up to limit entries older than t and returns how many it removed.
		DeleteBefore(ctx context.Context, t time.Time, limit int) (int, error)
	}

	ListActivityArgs struct {
		// FileID lists the entries of a file; with Folder also those of its direct children.
		FileID int
		Folder bool
		// OwnerID lists entries about this account (owner of the file or account).
		OwnerID int
		// UserID lists entries done by this account.
		UserID int
		// Types limits the result to these types; empty means all.
		Types []int
		// Account lists entries done by or about this account (admin filter).
		Account int
		// Before returns entries with an ID lower than this (cursor); 0 starts at the newest.
		Before   int
		PageSize int
	}

	ListActivityResult struct {
		Entries []*ent.Activity
		// Next is the cursor for the next page, 0 when there are no more entries.
		Next int
	}
)

func NewActivityClient(client *ent.Client) ActivityClient {
	return &activityClient{client: client}
}

type activityClient struct {
	client *ent.Client
}

func (c *activityClient) SetClient(newClient *ent.Client) TxOperator {
	return &activityClient{client: newClient}
}

func (c *activityClient) GetClient() *ent.Client {
	return c.client
}

func (c *activityClient) CreateBulk(ctx context.Context, entries []*ent.ActivityCreate) error {
	if len(entries) == 0 {
		return nil
	}

	return c.client.Activity.CreateBulk(entries...).Exec(ctx)
}

func (c *activityClient) List(ctx context.Context, args *ListActivityArgs) (*ListActivityResult, error) {
	pageSize := args.PageSize
	if pageSize <= 0 || pageSize > activityPageSizeMax {
		pageSize = activityPageSizeMax
	}

	where := make([]predicate.Activity, 0, 4)
	if args.FileID > 0 {
		if args.Folder {
			where = append(where, activity.Or(
				activity.FileID(args.FileID),
				activity.ParentID(args.FileID),
				activity.FromParentID(args.FileID),
			))
		} else {
			where = append(where, activity.FileID(args.FileID))
		}
	}
	if args.OwnerID > 0 {
		where = append(where, activity.OwnerID(args.OwnerID))
	}
	if args.UserID > 0 {
		where = append(where, activity.UserID(args.UserID))
	}
	if args.Account > 0 {
		where = append(where, activity.Or(activity.UserID(args.Account), activity.OwnerID(args.Account)))
	}
	if len(args.Types) > 0 {
		where = append(where, activity.TypeIn(args.Types...))
	}
	if args.Before > 0 {
		where = append(where, activity.IDLT(args.Before))
	}

	entries, err := c.client.Activity.Query().
		Where(where...).
		Order(activity.ByID(sql.OrderDesc())).
		Limit(pageSize + 1).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list activity: %w", err)
	}

	res := &ListActivityResult{Entries: entries}
	if len(entries) > pageSize {
		res.Entries = entries[:pageSize]
		res.Next = res.Entries[pageSize-1].ID
	}

	return res, nil
}

func (c *activityClient) DeleteBefore(ctx context.Context, t time.Time, limit int) (int, error) {
	ids, err := c.client.Activity.Query().
		Where(activity.CreatedAtLT(t)).
		Order(activity.ByID()).
		Limit(limit).
		IDs(ctx)
	if err != nil || len(ids) == 0 {
		return 0, err
	}

	return c.client.Activity.Delete().Where(activity.IDIn(ids...)).Exec(ctx)
}

// ensureActivityLog creates the activity table and its retention setting when they are missing.
// It runs on every start, because the schema migration only runs when the database version
// changes and Fylvo keeps the version of Cloudreve. Only this one table is touched.
func ensureActivityLog(l logging.Logger, client *ent.Client, ctx context.Context) error {
	if err := entmigrate.Create(ctx, client.Schema, []*entsqlschema.Table{entmigrate.ActivitiesTable}); err != nil {
		return fmt.Errorf("failed to create activity table: %w", err)
	}

	// A soft-deleted row still takes the name (unique index): it counts as present.
	n, err := client.Setting.Query().Where(setting.Name(ActivityRetentionSetting)).Count(schema.SkipSoftDelete(ctx))
	if err != nil {
		return fmt.Errorf("failed to query activity retention setting: %w", err)
	}
	if n == 0 {
		l.Info("Adding the setting %q with %s days.", ActivityRetentionSetting, ActivityRetentionDefault)
		if err := client.Setting.Create().
			SetName(ActivityRetentionSetting).
			SetValue(ActivityRetentionDefault).
			Exec(ctx); err != nil {
			return fmt.Errorf("failed to add activity retention setting: %w", err)
		}
	}

	return nil
}
