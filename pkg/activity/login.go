package activity

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/auth/requestinfo"
)

// Sign-in methods stored in ActivityContent.Method.
const (
	MethodPassword = "password"
	MethodTwoFA    = "2fa"
	MethodPasskey  = "passkey"
	MethodApp      = "app"
	MethodWebDAV   = "webdav"
)

// failureQuiet is how long the same failure of one account from one address is logged only
// once. WebDAV clients with an old password retry all the time.
const failureQuiet = 10 * time.Minute

// maxThrottleKeys caps the memory of the throttle; above it, old keys are dropped first.
const maxThrottleKeys = 10000

type throttle struct {
	mu   sync.Mutex
	now  func() time.Time
	seen map[string]time.Time
}

var failures = &throttle{now: time.Now, seen: make(map[string]time.Time)}

// allow reports whether key was not seen within failureQuiet, and marks it as seen.
func (t *throttle) allow(key string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := t.now()
	if last, ok := t.seen[key]; ok && now.Sub(last) < failureQuiet {
		return false
	}

	if len(t.seen) >= maxThrottleKeys {
		for k, last := range t.seen {
			if now.Sub(last) >= failureQuiet {
				delete(t.seen, k)
			}
		}
		if len(t.seen) >= maxThrottleKeys {
			return false
		}
	}

	t.seen[key] = now
	return true
}

// FailureWanted reports whether a failed sign-in of the given kind should be logged now. It is
// asked before looking up the account, so that a client retrying all the time costs nothing.
// subject is what the client sent (email, account ID).
func FailureWanted(ctx context.Context, typ Type, method, subject string) bool {
	ip := ""
	if info := requestinfo.RequestInfoFromContext(ctx); info != nil {
		ip = info.IP
	}
	return failures.allow(strconv.Itoa(int(typ)) + "|" + method + "|" + subject + "|" + ip)
}

// RecordAccount logs an entry about the account uid done by the account itself.
func RecordAccount(ctx context.Context, typ Type, uid int, method string) {
	Record(ctx, Entry{Type: typ, UserID: uid, OwnerID: uid, Content: types.ActivityContent{Method: method}})
}

// RecordFailure logs a failed or refused sign-in to the account uid. Who tried is unknown, so
// the entry has no actor.
func RecordFailure(ctx context.Context, typ Type, uid int, method string) {
	Record(ctx, Entry{Type: typ, OwnerID: uid, Content: types.ActivityContent{Method: method}})
}
