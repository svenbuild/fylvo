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
)
