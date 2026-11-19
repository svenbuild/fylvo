package inventory

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/cloudreve/Cloudreve/v4/ent"
	"golang.org/x/crypto/bcrypt"
)

func TestNewPasswordsUseBcrypt(t *testing.T) {
	digest, err := digestPassword("fylvo-local-test-1")
	if err != nil {
		t.Fatal(err)
	}
	u := &ent.User{Password: digest}
	if !isBcrypt(digest) || PasswordNeedsUpgrade(u) {
		t.Fatalf("new password should be stored with bcrypt, got %q", digest)
	}
	if CheckPassword(u, "fylvo-local-test-1") != nil {
		t.Fatal("the right password should be accepted")
	}
	if CheckPassword(u, "fylvo-local-test-2") == nil {
		t.Fatal("a wrong password must not be accepted")
	}
}

func TestOldSHA256PasswordStillWorks(t *testing.T) {
	salt := "abcdefghijklmnopqrstuvwxyz012345"
	sum := sha256.Sum256([]byte("altes-passwort" + salt))
	u := &ent.User{Password: salt + ":" + hex.EncodeToString(sum[:])}
	if CheckPassword(u, "altes-passwort") != nil {
		t.Fatal("a password stored by Cloudreve should still be accepted")
	}
	if CheckPassword(u, "falsch") == nil {
		t.Fatal("a wrong password must not be accepted")
	}
	if !PasswordNeedsUpgrade(u) {
		t.Fatal("a password stored by Cloudreve should be upgraded")
	}
}

func TestLongPasswordsDifferAfter72Bytes(t *testing.T) {
	long := strings.Repeat("a", 100)
	digest, err := digestPassword(long + "1")
	if err != nil {
		t.Fatal(err)
	}
	if CheckPassword(&ent.User{Password: digest}, long+"2") == nil {
		t.Fatal("passwords that differ after 72 bytes must not match")
	}
}

func TestDummyDigestMatchesCost(t *testing.T) {
	cost, err := bcrypt.Cost([]byte(dummyDigest))
	if err != nil || cost != bcryptCost {
		t.Fatalf("the stand-in hash should use cost %d, got %d (%v)", bcryptCost, cost, err)
	}
}
