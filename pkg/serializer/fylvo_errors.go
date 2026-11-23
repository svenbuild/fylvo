package serializer

import "errors"

// KeepAppError returns err when it already is an AppError (for example a check that failed
// inside a settings change), and wraps anything else with the given code and message.
func KeepAppError(err error, code int, msg string) error {
	var appErr AppError
	if errors.As(err, &appErr) {
		return err
	}

	return NewError(code, msg, err)
}
