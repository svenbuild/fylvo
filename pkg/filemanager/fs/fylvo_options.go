package fs

import "time"

// WithModifiedAt gives a new folder this date as creation and change date instead of now.
// Fylvo: the import of an existing folder uses it to keep the dates of the folders on disk.
func WithModifiedAt(t time.Time) Option {
	return OptionFunc(func(o *FsOption) {
		if !t.IsZero() {
			o.ModifiedAt = &t
		}
	})
}
