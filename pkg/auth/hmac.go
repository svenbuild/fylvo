package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
)

// HMACAuth HMAC algorithm authentication
type HMACAuth struct {
	SecretKey []byte
}

// Sign generates a signature for the given body that expires at expires, a timestamp,
// 0 means no expiry
func (auth HMACAuth) Sign(body string, expires int64) string {
	h := hmac.New(sha256.New, auth.SecretKey)
	expireTimeStamp := strconv.FormatInt(expires, 10)
	_, err := io.WriteString(h, body+":"+expireTimeStamp)
	if err != nil {
		return ""
	}

	return base64.URLEncoding.EncodeToString(h.Sum(nil)) + ":" + expireTimeStamp
}

// Check verifies the given body and signature, including the expires check
func (auth HMACAuth) Check(body string, sign string) error {
	signSlice := strings.Split(sign, ":")
	// if the expires field is missing
	if signSlice[len(signSlice)-1] == "" {
		return ErrExpiresMissing
	}

	// check whether it has expired
	expires, err := strconv.ParseInt(signSlice[len(signSlice)-1], 10, 64)
	if err != nil {
		return serializer.NewError(serializer.CodeInvalidSign, "sign expired", nil)
	}
	// if the signature has expired
	if expires < time.Now().Unix() && expires != 0 {
		return ErrExpired
	}

	// verify signature
	if subtle.ConstantTimeCompare([]byte(auth.Sign(body, expires)), []byte(sign)) != 1 {
		return serializer.NewError(serializer.CodeInvalidSign, "invalid sign", nil)
	}
	return nil
}
