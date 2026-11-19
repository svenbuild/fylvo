package serializer

// Error codes added by Fylvo. They start at 40900 so that new codes from upstream do not
// collide with them.
const (
	// CodeTooManyAttempts too many wrong passwords or codes, try again later
	CodeTooManyAttempts = 40900
)
