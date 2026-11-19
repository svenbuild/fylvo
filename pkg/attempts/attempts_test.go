package attempts

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/cloudreve/Cloudreve/v4/pkg/auth/requestinfo"
)

func newTestLimiter() (*Limiter, *time.Time) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	l := New(MaxFailures, Window)
	l.now = func() time.Time { return now }
	return l, &now
}

// wrong records n wrong guesses, each allowed by Check first.
func wrong(t *testing.T, l *Limiter, key string, n int) {
	t.Helper()
	for i := 1; i <= n; i++ {
		if err := l.Check(key); err != nil {
			t.Fatalf("try %d refused too early", i)
		}
		_ = l.Done(key, false)
	}
}

func TestEleventhTryIsRefused(t *testing.T) {
	l, _ := newTestLimiter()
	wrong(t, l, "k", MaxFailures)
	if err := l.Check("k"); err != ErrTooManyAttempts {
		t.Fatalf("try %d should be refused, got %v", MaxFailures+1, err)
	}
	if err := l.Done("k", true); err != ErrTooManyAttempts {
		t.Fatal("a right guess must be refused while blocked")
	}
}

func TestBlockEndsAfterWindow(t *testing.T) {
	l, now := newTestLimiter()
	wrong(t, l, "k", MaxFailures)
	*now = now.Add(Window - time.Second)
	if l.Check("k") == nil {
		t.Fatal("should still be blocked just before the window ends")
	}
	*now = now.Add(time.Second)
	if l.Check("k") != nil || l.Done("k", true) != nil {
		t.Fatal("a right guess should work once the window has passed")
	}
}

func TestSlowGuessesDoNotBlock(t *testing.T) {
	l, now := newTestLimiter()
	for i := 0; i < 3*MaxFailures; i++ {
		wrong(t, l, "k", 1)
		*now = now.Add(Window)
	}
}

func TestRightGuessClearsCount(t *testing.T) {
	l, _ := newTestLimiter()
	wrong(t, l, "k", MaxFailures-1)
	if err := l.Done("k", true); err != nil {
		t.Fatal(err)
	}
	wrong(t, l, "k", MaxFailures)
}

// Many wrong guesses in parallel with one right guess: the right one may only pass if fewer
// than MaxFailures wrong ones were recorded before it.
func TestParallelGuesses(t *testing.T) {
	l, _ := newTestLimiter()
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if l.Check("k") == nil {
				_ = l.Done("k", false)
			}
		}()
	}
	wg.Wait()
	if l.entries["k"].failures < MaxFailures {
		t.Fatalf("only %d wrong guesses recorded", l.entries["k"].failures)
	}
	if l.Done("k", true) == nil {
		t.Fatal("a right guess after ten recorded wrong ones must be refused")
	}
}

func TestKeyPerAddressAndSubject(t *testing.T) {
	at := func(ip string) context.Context {
		return context.WithValue(context.Background(), requestinfo.RequestInfoCtx{}, &requestinfo.RequestInfo{IP: ip})
	}
	l, _ := newTestLimiter()
	wrong(t, l, Key(at("1.1.1.1"), KindLogin, "Sven@Example.org"), MaxFailures)

	if l.Check(Key(at("1.1.1.1"), KindLogin, "sven@example.org")) == nil {
		t.Fatal("subject should be compared without case")
	}
	if l.Check(Key(at("2.2.2.2"), KindLogin, "sven@example.org")) != nil {
		t.Fatal("another address must not be blocked")
	}
	if l.Check(Key(at("1.1.1.1"), KindWebDAV, "sven@example.org")) != nil {
		t.Fatal("another kind of secret must not be blocked")
	}
}

func TestIPv6CountsPerNetwork(t *testing.T) {
	a := Key(context.WithValue(context.Background(), requestinfo.RequestInfoCtx{},
		&requestinfo.RequestInfo{IP: "2001:db8:1:2::1"}), KindLogin, "x")
	b := Key(context.WithValue(context.Background(), requestinfo.RequestInfoCtx{},
		&requestinfo.RequestInfo{IP: "2001:db8:1:2:ffff::9"}), KindLogin, "x")
	c := Key(context.WithValue(context.Background(), requestinfo.RequestInfoCtx{},
		&requestinfo.RequestInfo{IP: "2001:db8:1:3::1"}), KindLogin, "x")
	if a != b {
		t.Fatal("addresses in the same /64 must share a key")
	}
	if a == c {
		t.Fatal("addresses in different /64 networks must not share a key")
	}
}

func TestExpiredKeysArePruned(t *testing.T) {
	l, now := newTestLimiter()
	for i := 0; i < 100; i++ {
		_ = l.Done(fmt.Sprintf("old-%d", i), false)
	}
	*now = now.Add(Window)
	_ = l.Done("new", false)
	if len(l.entries) != 1 {
		t.Fatalf("expired keys should be pruned, %d left", len(l.entries))
	}
}
