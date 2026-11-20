package auth

import (
	"bytes"
	"errors"

	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/gin-gonic/gin"
)

// Fylvo: access tokens carry the same hash of the account state as refresh tokens (e-mail and
// stored password). A new password therefore ends the access tokens of other sessions at once,
// not only when they expire.

// ErrPasswordChangeRequired is returned by Issue for an account that has to set a new password.
var ErrPasswordChangeRequired = errors.New("the password has to be changed first")

type accessStateHashCtx struct{}

// UserStateValid reports whether the access token of this request was issued for the current
// password of u. Requests without such a hash (no token, or a token issued before this check
// existed) are valid.
func (t *tokenAuth) UserStateValid(c *gin.Context, u *ent.User) bool {
	stateHash, _ := c.Value(accessStateHashCtx{}).([]byte)
	if len(stateHash) == 0 {
		return true
	}
	expected := t.hashUserState(c, u)
	return bytes.Equal(stateHash, expected[:])
}
