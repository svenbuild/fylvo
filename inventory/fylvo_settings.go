package inventory

import (
	"context"
	"fmt"
	"sync"

	"github.com/cloudreve/Cloudreve/v4/ent/user"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
)

// Fylvo: the settings of an account are one JSON value. Several places change parts of it (pins,
// views, preferences, favorites, the password-change flag). Each of them used to write the copy
// it loaded at the start of the request, so two requests at the same time could undo each other.
// Every writer now takes settingsMu, reads the stored value fresh and changes only its own part.
// One lock for all accounts is enough: there is one server process and the writes are short.
var settingsMu sync.Mutex

// UpdateSettings changes the stored settings of an account. change gets the fresh value and may
// modify it; when it returns an error nothing is written. Returns the stored result.
func (c *userClient) UpdateSettings(ctx context.Context, uid int, change func(*types.UserSetting) error) (*types.UserSetting, error) {
	settingsMu.Lock()
	defer settingsMu.Unlock()

	settings, err := c.freshSettings(ctx, uid)
	if err != nil {
		return nil, err
	}

	if err := change(settings); err != nil {
		return nil, err
	}

	if err := c.client.User.UpdateOneID(uid).SetSettings(settings).Exec(ctx); err != nil {
		return nil, fmt.Errorf("failed to save settings of user %d: %w", uid, err)
	}

	return settings, nil
}

// freshSettings reads the stored settings of an account; the caller holds settingsMu.
func (c *userClient) freshSettings(ctx context.Context, uid int) (*types.UserSetting, error) {
	u, err := c.client.User.Query().Where(user.ID(uid)).Select(user.FieldSettings).Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read settings of user %d: %w", uid, err)
	}

	if u.Settings == nil {
		return &types.UserSetting{}, nil
	}

	return u.Settings, nil
}
