package mutex

import (
	"strings"
	"testing"
)

func mustPanic(t *testing.T, name string, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("%s: expected a panic, got none", name)
		}
	}()
	fn()
}

// An invalid key must fail fast rather than spin the keep-trying loop forever.
// The check runs before any network, so no server is needed here.
func TestCheckKeyRejectsInvalid(t *testing.T) {
	mustPanic(t, "empty", func() { checkKey("") })
	mustPanic(t, "too long", func() { checkKey(strings.Repeat("k", maxValueSize+1)) })

	// exactly at the limit and a normal key are fine
	checkKey(strings.Repeat("k", maxValueSize))
	checkKey("locking-me")
}

// The public methods validate before entering their retry loops.
func TestLockFailsFastOnInvalidKey(t *testing.T) {
	lc := &lockingCenter{} // no server, but validation happens first
	mustPanic(t, "Lock empty", func() { lc.Lock("") })
	mustPanic(t, "Unlock too long", func() { lc.Unlock(strings.Repeat("k", 200)) })
	mustPanic(t, "ResetByKey empty", func() { lc.ResetByKey("") })
}

func TestConstructorRejectsLongSourceAddr(t *testing.T) {
	long := strings.Repeat("s", maxValueSize+1)
	if _, err := NewLockingCenterWithSourceAddr("127.0.0.1:22119", &long); err == nil {
		t.Error("expected an error for an over-long source address")
	}
}
