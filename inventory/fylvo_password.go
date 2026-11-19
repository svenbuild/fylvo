package inventory

import (
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"sync"

	"github.com/cloudreve/Cloudreve/v4/ent"
	"golang.org/x/crypto/bcrypt"
)

// Fylvo stores passwords with bcrypt. Passwords stored by Cloudreve (salted SHA-256, SHA-1 or
// MD5) still work and are replaced with bcrypt at the next sign-in (see service/user/login.go).

// bcryptCost is the work factor: about a tenth of a second per check on a small server.
const bcryptCost = 11

// bcryptDigest hashes a password with bcrypt. The password is first hashed with SHA-256,
// because bcrypt only reads 72 bytes and Fylvo allows longer passwords.
func bcryptDigest(password string) (string, error) {
	digest, err := bcrypt.GenerateFromPassword(bcryptInput(password), bcryptCost)
	return string(digest), err
}

func bcryptInput(password string) []byte {
	sum := sha256.Sum256([]byte(password))
	return []byte(base64.StdEncoding.EncodeToString(sum[:]))
}

// isBcrypt reports whether a stored password is a bcrypt hash. The older formats start with a
// random salt of letters and digits, never with "$".
func isBcrypt(stored string) bool {
	return strings.HasPrefix(stored, "$2")
}

func checkBcrypt(stored, password string) error {
	if bcrypt.CompareHashAndPassword([]byte(stored), bcryptInput(password)) != nil {
		return ErrorIncorrectPassword
	}
	return nil
}

// PasswordNeedsUpgrade reports whether the password of u is stored in an older, fast format.
func PasswordNeedsUpgrade(u *ent.User) bool {
	return u.Password != "" && !isBcrypt(u.Password)
}

var (
	dummyDigest     string
	dummyDigestOnce sync.Once
)

// CheckPasswordWithoutUser takes as long as checking a real password. Sign-in calls it for an
// unknown e-mail address, so that the answer time does not tell whether the account exists.
func CheckPasswordWithoutUser(password string) {
	dummyDigestOnce.Do(func() {
		dummyDigest, _ = bcryptDigest("fylvo-no-such-user")
	})
	_ = checkBcrypt(dummyDigest, password)
}
