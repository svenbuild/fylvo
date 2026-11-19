package attempts

import (
	"context"
	"fmt"
	"strings"
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

// from returns a context with the given client address.
func from(ip string) context.Context {
	return context.WithValue(context.Background(), requestinfo.RequestInfoCtx{}, &requestinfo.RequestInfo{IP: ip})
}

var k = KeyFor(from("1.1.1.1"), KindLogin, "sven@example.org")

// wrong records n wrong guesses, each allowed by Check first.
func wrong(t *testing.T, l *Limiter, key Key, n int) {
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
	wrong(t, l, k, MaxFailures)
	if err := l.Check(k); err != ErrTooManyAttempts {
		t.Fatalf("try %d should be refused, got %v", MaxFailures+1, err)
	}
	if err := l.Done(k, true); err != ErrTooManyAttempts {
		t.Fatal("a right guess must be refused while blocked")
	}
}

func TestBlockEndsAfterWindow(t *testing.T) {
	l, now := newTestLimiter()
	wrong(t, l, k, MaxFailures)
	*now = now.Add(Window - time.Second)
	if l.Check(k) == nil {
		t.Fatal("should still be blocked just before the window ends")
	}
	*now = now.Add(time.Second)
	if l.Check(k) != nil || l.Done(k, true) != nil {
		t.Fatal("a right guess should work once the window has passed")
	}
}

func TestSlowGuessesDoNotBlock(t *testing.T) {
	l, now := newTestLimiter()
	for i := 0; i < 3*MaxFailures; i++ {
		wrong(t, l, k, 1)
		*now = now.Add(Window)
	}
}

func TestRightGuessClearsCount(t *testing.T) {
	l, _ := newTestLimiter()
	wrong(t, l, k, MaxFailures-1)
	if err := l.Done(k, true); err != nil {
		t.Fatal(err)
	}
	wrong(t, l, k, MaxFailures)
}

// Many wrong guesses in parallel: once ten are recorded, a right guess is refused.
func TestParallelGuesses(t *testing.T) {
	l, _ := newTestLimiter()
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if l.Check(k) == nil {
				_ = l.Done(k, false)
			}
		}()
	}
	wg.Wait()
	if l.entries[k.subject].failures < MaxFailures {
		t.Fatalf("only %d wrong guesses recorded", l.entries[k.subject].failures)
	}
	if l.Done(k, true) == nil {
		t.Fatal("a right guess after ten recorded wrong ones must be refused")
	}
}

func TestKeyPerAddressAndSubject(t *testing.T) {
	l, _ := newTestLimiter()
	wrong(t, l, KeyFor(from("1.1.1.1"), KindLogin, "Sven@Example.org"), MaxFailures)

	if l.Check(KeyFor(from("1.1.1.1"), KindLogin, "sven@example.org")) == nil {
		t.Fatal("subject should be compared without case")
	}
	if l.Check(KeyFor(from("2.2.2.2"), KindLogin, "sven@example.org")) != nil {
		t.Fatal("another address must not be blocked")
	}
	if l.Check(KeyFor(from("1.1.1.1"), KindWebDAV, "sven@example.org")) != nil {
		t.Fatal("another kind of secret must not be blocked")
	}
	if l.Check(KeyFor(from("1.1.1.1"), KindLogin, "other@example.org")) != nil {
		t.Fatal("another account must not be blocked")
	}
}

func TestIPv6CountsPerNetwork(t *testing.T) {
	a := KeyFor(from("2001:db8:1:2::1"), KindLogin, "x")
	b := KeyFor(from("2001:db8:1:2:ffff::9"), KindLogin, "x")
	c := KeyFor(from("2001:db8:1:3::1"), KindLogin, "x")
	if a != b {
		t.Fatal("addresses in the same /64 must share a key")
	}
	if a == c {
		t.Fatal("addresses in different /64 networks must not share a key")
	}
}

func TestLongSubjectTakesFixedSpace(t *testing.T) {
	key := KeyFor(from("1.1.1.1"), KindWebDAV, strings.Repeat("x", 1<<20))
	if len(key.subject) > 64 || len(key.address) > 64 {
		t.Fatal("keys must have a fixed size")
	}
}

func TestExpiredKeysArePruned(t *testing.T) {
	l, now := newTestLimiter()
	for i := 0; i < 100; i++ {
		_ = l.Done(KeyFor(from("1.1.1.1"), KindLogin, fmt.Sprint(i)), false)
	}
	*now = now.Add(Window)
	_ = l.Done(k, false)
	if len(l.entries) != 1 {
		t.Fatalf("expired keys should be pruned, %d left", len(l.entries))
	}
}

// When the map is full, wrong guesses for new subjects still count, per address.
func TestFullMapStillLimits(t *testing.T) {
	l, now := newTestLimiter()
	for i := 0; i < maxKeys; i++ {
		l.entries[fmt.Sprint(i)] = &entry{failures: 1, until: l.now().Add(Window)}
	}
	attacker := func(i int) Key { return KeyFor(from("6.6.6.6"), KindLogin, fmt.Sprint("junk", i)) }
	for i := 0; i < MaxFailures; i++ {
		if l.Check(attacker(i)) != nil {
			t.Fatalf("try %d refused too early", i+1)
		}
		_ = l.Done(attacker(i), false)
	}
	if l.Check(attacker(MaxFailures)) == nil {
		t.Fatal("the address should be blocked once the map is full")
	}
	if l.Check(KeyFor(from("7.7.7.7"), KindLogin, "sven@example.org")) != nil {
		t.Fatal("another address must not be blocked")
	}

	// Above twice the cap, keys not counted yet are refused.
	for i := maxKeys; i < 2*maxKeys; i++ {
		l.entries[fmt.Sprint(i)] = &entry{failures: 1, until: l.now().Add(Window)}
	}
	if l.Check(KeyFor(from("8.8.8.8"), KindLogin, "sven@example.org")) == nil {
		t.Fatal("a new key should be refused above twice the cap")
	}

	// Once the old keys expire, new keys work again, without any further wrong guess.
	*now = now.Add(Window)
	if l.Check(KeyFor(from("8.8.8.8"), KindLogin, "sven@example.org")) != nil {
		t.Fatal("expired keys should be pruned before the cap is checked")
	}
}

func TestCheckSecret(t *testing.T) {
	ctx := from("9.9.9.9")
	if right, err := CheckSecret(ctx, KindShare, "1", "", "geheim"); right || err != nil {
		t.Fatal("an empty secret is no guess")
	}
	if right, _ := CheckSecret(ctx, KindShare, "1", "geheim", "geheim"); !right {
		t.Fatal("the right secret should be accepted")
	}
	if right, _ := CheckSecret(ctx, KindShare, "1", "falsch", "geheim"); right {
		t.Fatal("a wrong secret must not be accepted")
	}
}
