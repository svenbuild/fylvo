package types

// ActivityContent holds the details of an activity log entry that do not need their own column.
// Names are copied at the time of the action, because the file or folder can be gone later.
type ActivityContent struct {
	// Name of the file or folder (after the action).
	Name string `json:"name,omitempty"`
	// OldName is the name before a rename.
	OldName string `json:"old_name,omitempty"`
	// IsFolder is true when the entry is about a folder.
	IsFolder bool `json:"is_folder,omitempty"`
	// FromFolder and ToFolder are the folder names of a move, copy or restore.
	FromFolder string `json:"from_folder,omitempty"`
	ToFolder   string `json:"to_folder,omitempty"`
	// Method of a sign-in: password, 2fa, passkey, app, webdav.
	Method string `json:"method,omitempty"`
	// Target is a free text detail, for example the account an admin changed.
	Target string `json:"target,omitempty"`
}
