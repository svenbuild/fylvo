package email

import (
	"context"
	"errors"
)

// Driver mail sending driver
type Driver interface {
	// Close closes the driver
	Close()
	// Send sends an email
	Send(ctx context.Context, to, title, body string) error
}

var (
	// ErrChanNotOpen mail queue is not enabled
	ErrChanNotOpen = errors.New("email queue is not started")
	// ErrNoActiveDriver no mail sending service available
	ErrNoActiveDriver = errors.New("no avaliable email provider")
)
