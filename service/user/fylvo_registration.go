package user

import (
	"context"

	"github.com/cloudreve/Cloudreve/v4/application/dependency"
)

// RegistrationOpen reports whether visitors may sign up (Fylvo). Fylvo ships with sign-up
// closed, but it stays open while there is no account at all, so that the first account, which
// becomes the admin, can still be created on a new installation.
func RegistrationOpen(ctx context.Context) bool {
	dep := dependency.FromContext(ctx)
	if dep.SettingProvider().RegisterEnabled(ctx) {
		return true
	}

	count, err := dep.UserClient().CountByTimeRange(ctx, nil, nil)
	return err == nil && count == 0
}
