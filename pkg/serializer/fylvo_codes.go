package serializer

// Error codes added by Fylvo. They start at 40900 so that new codes from upstream do not
// collide with them.
const (
	// CodeTooManyAttempts too many wrong passwords or codes, try again later
	CodeTooManyAttempts = 40900
	// CodePasswordChangeRequired the password was right, but the account has to set a new
	// password first; data holds the ticket for POST /session/token/password
	CodePasswordChangeRequired = 40901
	// CodeSamePassword the new password is the same as the current one
	CodeSamePassword = 40902
	// CodePasswordChangeExpired the ticket of the password change is unknown or expired
	CodePasswordChangeExpired = 40903
	// CodeUploadTargetGone an upload that names the version it replaces ("previous") found no
	// file at its address: the file was deleted or moved away
	CodeUploadTargetGone = 40904
	// CodeNameCaseTaken the folder holds a file or folder whose name differs from the given
	// one only in upper and lower case; Windows cannot hold both
	CodeNameCaseTaken = 40905

	// 40920 to 40929: connected storages (pkg/connected).

	// CodeConnectedUnsupported the action does not exist in a connected storage
	CodeConnectedUnsupported = 40920
	// CodeConnectedReconnect the provider no longer accepts the stored access; the account
	// has to connect the storage again
	CodeConnectedReconnect = 40921
	// CodeConnectedUnavailable connected storages cannot be used: the provider is not set
	// up, refuses the installation's client, or the group of the account may not use them
	CodeConnectedUnavailable = 40922
	// CodeConnectedBusy the provider limits requests or did not answer in time
	CodeConnectedBusy = 40923
	// CodeConnectedFull the storage at the provider is full
	CodeConnectedFull = 40924
	// CodeConnectedItem this item cannot be used; data holds the reason
	CodeConnectedItem = 40925
	// CodeConnectedListingGone the listing a page token belongs to is no longer kept
	CodeConnectedListingGone = 40926
	// CodeConnectedOutcomeUnknown a save was sent but the provider did not confirm it
	CodeConnectedOutcomeUnknown = 40927
)
