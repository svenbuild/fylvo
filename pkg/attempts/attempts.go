// Package attempts limits wrong guesses of secrets (Fylvo): passwords at sign-in, 2FA codes,
// WebDAV passwords and share link passwords.
//
// Wrong guesses are counted per kind of secret, per subject (account, share) and per client
// address (IPv6 per /64 network). After MaxFailures wrong guesses, every further try for that
// key is refused, even a right one, until Window has passed since the last wrong guess. A right
// guess clears the count. Counting per address means a stranger cannot lock the owner out from
// somewhere else, as long as the server knows the real client address (see
// routers/fylvo_client_ip.go).
//
// The counters live in memory: a restart clears them. That is enough for a private instance.
package attempts

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
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

	// maxKeys caps the memory use. Above it, wrong guesses for new subjects are counted per
	// address alone, so that filling the map does not switch the limit off. Above twice as many
	// keys, tries with keys not counted yet are refused until old keys expire.
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

// Key identifies the counts for one try: per subject and address, and per address alone.
type Key struct {
	subject string
	address string
}

// KeyFor builds the key for one kind of secret and one subject, from the client address stored
// in ctx by middleware.InitializeHandling. The parts are hashed, so a long subject sent by a
// client does not take more memory.
func KeyFor(ctx context.Context, kind, subject string) Key {
	ip := ""
	if info := requestinfo.RequestInfoFromContext(ctx); info != nil {
		ip = addressGroup(info.IP)
	}
	return Key{
		subject: hash(kind, strings.ToLower(subject), ip),
		address: hash(kind, ip),
	}
}

func hash(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:16])
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
func (l *Limiter) Check(key Key) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if l.blocked(key.subject, now) || l.blocked(key.address, now) {
		return ErrTooManyAttempts
	}
	l.prune(now)
	if len(l.entries) >= 2*maxKeys && !l.live(key.subject, now) && !l.live(key.address, now) {
		return ErrTooManyAttempts
	}
	return nil
}

// Done records the result of one check of the secret. A wrong guess is counted. A right guess
// clears the count, unless the key is blocked by now: then Done returns ErrTooManyAttempts and
// the right guess must be refused too. That second look makes parallel guesses harmless: a
// right guess only counts if fewer than MaxFailures wrong ones were recorded before it.
func (l *Limiter) Done(key Key, right bool) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if right {
		if l.blocked(key.subject, now) || l.blocked(key.address, now) {
			return ErrTooManyAttempts
		}
		delete(l.entries, key.subject)
		delete(l.entries, key.address)
		return nil
	}

	l.prune(now)
	id := key.subject
	if !l.live(id, now) && len(l.entries) >= maxKeys {
		id = key.address
	}
	e := l.entries[id]
	if !l.live(id, now) {
		if len(l.entries) >= 2*maxKeys {
			return nil
		}
		e = &entry{}
		l.entries[id] = e
	}
	e.failures++
	e.until = now.Add(l.window)
	return nil
}

// Guard checks a secret with verify under the limit of Default.
func Guard(ctx context.Context, kind, subject string, verify func() bool) (bool, error) {
	key := KeyFor(ctx, kind, subject)
	if err := Default.Check(key); err != nil {
		return false, err
	}
	right := verify()
	if err := Default.Done(key, right); err != nil {
		return false, err
	}
	return right, nil
}

// CheckSecret compares a secret sent by the client with the expected one, under the limit of
// Default. An empty secret is no guess: it is not counted and is not right.
func CheckSecret(ctx context.Context, kind, subject, given, want string) (bool, error) {
	if given == "" {
		return false, nil
	}
	return Guard(ctx, kind, subject, func() bool {
		return subtle.ConstantTimeCompare([]byte(given), []byte(want)) == 1
	})
}

func (l *Limiter) live(id string, now time.Time) bool {
	e, ok := l.entries[id]
	return ok && now.Before(e.until)
}

func (l *Limiter) blocked(id string, now time.Time) bool {
	return l.live(id, now) && l.entries[id].failures >= l.maxFailures
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
