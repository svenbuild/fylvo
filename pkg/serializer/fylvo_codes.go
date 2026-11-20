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
)
