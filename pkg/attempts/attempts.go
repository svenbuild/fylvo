// Package attempts limits wrong guesses of secrets (Fylvo): passwords at sign-in, 2FA codes,
// WebDAV passwords and share link passwords.
//
// Tries are counted per kind of secret, per subject (account, share) and per client address
// (IPv6 per /64 network). After MaxFailures wrong guesses, every further try for that key is
// refused, even a right one, until Window has passed since the last wrong guess. A right guess
// clears the count. Counting per address means a stranger cannot lock the
// owner out from somewhere else, as long as the server knows the real client address (see
// routers/fylvo_client_ip.go).
//
// The counters live in memory: a restart clears them. That is enough for a private instance.
package attempts

import (
	"context"
	"crypto/subtle"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/cloudreve/Cloudreve/v4/pkg/auth/requestinfo"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
)

const (
	// MaxFailures is the number of wrong guesses allowed before a key is blocked.
	MaxFailures = 10
	// Window is how long a key stays blocked after its last wrong guess.
	Window = 15 * time.Minute

	// Kinds of secrets, each counted on its own.
	KindLogin  = "login"
	KindTwoFA  = "2fa"
	KindWebDAV = "webdav"
	KindShare  = "share"

	// maxKeys caps the memory use. When it is reached, new keys are not counted until old
	// ones expire; keys already counted keep their state.
	maxKeys = 100000
	// pruneEvery is how often expired keys are dropped.
	pruneEvery = time.Minute
)

// Limiter counts wrong guesses per key.
type Limiter struct {
	mu          sync.Mutex
	maxFailures int
	window      time.Duration
	now         func() time.Time
	entries     map[string]*entry
	lastPrune   time.Time
}

type entry struct {
	failures int
	until    time.Time
}

// New returns a limiter that blocks a key after maxFailures wrong guesses for window.
func New(maxFailures int, window time.Duration) *Limiter {
	return &Limiter{
		maxFailures: maxFailures,
		window:      window,
		now:         time.Now,
		entries:     make(map[string]*entry),
	}
}

// Default is the limiter the server uses.
var Default = New(MaxFailures, Window)

// ErrTooManyAttempts is returned while a key is blocked.
var ErrTooManyAttempts = serializer.NewError(serializer.CodeTooManyAttempts,
	"Too many wrong attempts, please try again later", nil)

// Key builds the key for one kind of secret and one subject, from the client address stored
// in ctx by middleware.InitializeHandling.
func Key(ctx context.Context, kind, subject string) string {
	ip := ""
	if info := requestinfo.RequestInfoFromContext(ctx); info != nil {
		ip = addressGroup(info.IP)
	}
	return kind + "\x00" + strings.ToLower(subject) + "\x00" + ip
}

// addressGroup returns an IPv4 address as is and an IPv6 address as its /64 network, because
// one IPv6 client usually owns a whole /64.
func addressGroup(ip string) string {
	parsed := net.ParseIP(ip)
	if parsed == nil || parsed.To4() != nil {
		return ip
	}
	return parsed.Mask(net.CIDRMask(64, 128)).String() + "/64"
}

// Check returns ErrTooManyAttempts while key has used up its tries. Call it before checking
// the secret, then report the result with Done.
func (l *Limiter) Check(key string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.blocked(key, l.now()) {
		return ErrTooManyAttempts
	}
	return nil
}

// Done records the result of one check of the secret. A wrong guess is counted. A right guess
// clears the count, unless the key is blocked by now: then Done returns ErrTooManyAttempts and
// the right guess must be refused too. That second look makes parallel guesses harmless: a
// right guess only counts if fewer than MaxFailures wrong ones were recorded before it.
func (l *Limiter) Done(key string, right bool) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if right {
		if l.blocked(key, now) {
			return ErrTooManyAttempts
		}
		delete(l.entries, key)
		return nil
	}

	l.prune(now)
	e, ok := l.entries[key]
	if !ok || !now.Before(e.until) {
		if len(l.entries) >= maxKeys {
			return nil
		}
		e = &entry{}
		l.entries[key] = e
	}
	e.failures++
	e.until = now.Add(l.window)
	return nil
}

func (l *Limiter) blocked(key string, now time.Time) bool {
	e, ok := l.entries[key]
	return ok && e.failures >= l.maxFailures && now.Before(e.until)
}

// CheckSecret compares a secret sent by the client with the expected one, under the limit of
// Default. An empty secret is no guess: it is not counted and is not right.
func CheckSecret(ctx context.Context, kind, subject, given, want string) (bool, error) {
	if given == "" {
		return false, nil
	}

	key := Key(ctx, kind, subject)
	if err := Default.Check(key); err != nil {
		return false, err
	}
	right := subtle.ConstantTimeCompare([]byte(given), []byte(want)) == 1
	if err := Default.Done(key, right); err != nil {
		return false, err
	}
	return right, nil
}

// prune drops expired keys, at most once per pruneEvery. The caller holds l.mu.
func (l *Limiter) prune(now time.Time) {
	if now.Sub(l.lastPrune) < pruneEvery {
		return
	}
	l.lastPrune = now
	for k, e := range l.entries {
		if !now.Before(e.until) {
			delete(l.entries, k)
		}
	}
}
