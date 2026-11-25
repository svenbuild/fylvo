package onedrive

import "time"

// Option extra settings for sending requests
type Option interface {
	apply(*options)
}

type options struct {
	redirect          string
	code              string
	refreshToken      string
	conflictBehavior  string
	expires           time.Time
	useDriverResource bool
}

type optionFunc func(*options)

// WithCode sets the API Code
func WithCode(t string) Option {
	return optionFunc(func(o *options) {
		o.code = t
	})
}

// WithRefreshToken sets the API RefreshToken
func WithRefreshToken(t string) Option {
	return optionFunc(func(o *options) {
		o.refreshToken = t
	})
}

// WithConflictBehavior sets how file name conflicts are handled
func WithConflictBehavior(t string) Option {
	return optionFunc(func(o *options) {
		o.conflictBehavior = t
	})
}

// WithConflictBehavior sets how file name conflicts are handled
func WithDriverResource(t bool) Option {
	return optionFunc(func(o *options) {
		o.useDriverResource = t
	})
}

func (f optionFunc) apply(o *options) {
	f(o)
}

func newDefaultOption() *options {
	return &options{
		conflictBehavior:  "fail",
		useDriverResource: true,
		expires:           time.Now().UTC().Add(time.Duration(1) * time.Hour),
	}
}
