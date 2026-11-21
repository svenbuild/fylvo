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

// WithCopyName gives the copy of a single file or folder another name than its source.
// Fylvo: a WebDAV COPY names its destination. Copying under the old name first and renaming
// after failed whenever the destination folder already held that name, as in a copy within
// the same folder.
func WithCopyName(name string) Option {
	return OptionFunc(func(o *FsOption) {
		o.CopyName = name
	})
}
