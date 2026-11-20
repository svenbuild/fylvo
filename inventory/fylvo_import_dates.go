package inventory

import "time"

// Fylvo: importDate is the date of the file on disk when a file is imported from the storage
// policy (admin "import external folder"), nil for everything else.
func importDate(args *EntityParameters) *time.Time {
	if args == nil || !args.Importing || args.ModifiedAt == nil || args.ModifiedAt.IsZero() {
		return nil
	}

	return args.ModifiedAt
}
