package mutex

import (
	"io"
	"net"
	"testing"
	"time"
)

// fakeServer answers every request with one fixed byte, after draining the
// request, so the client's TryLock response handling can be tested without the
// real server. It records the action byte of each request so a test can assert
// the client sent the try-lock action rather than the blocking lock action.
func fakeServer(t *testing.T, reply byte) (string, chan byte, func()) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("unable to listen: %s", err)
	}
	actions := make(chan byte, 16)

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()

				action := make([]byte, 1)
				if _, err := io.ReadFull(c, action); err != nil {
					return // the ping connection, closed without a request
				}
				// a lock/try request is key then source, both length prefixed
				for i := 0; i < 2; i++ {
					size := make([]byte, 1)
					if _, err := io.ReadFull(c, size); err != nil {
						return
					}
					if _, err := io.ReadFull(c, make([]byte, int(size[0]))); err != nil {
						return
					}
				}
				actions <- action[0]
				_, _ = c.Write([]byte{reply})
			}(conn)
		}
	}()

	return ln.Addr().String(), actions, func() { _ = ln.Close() }
}

// A held key answers '#'. TryLock must return false, promptly, and it must send
// the try-lock action (5), not the blocking lock action.
func TestTryLockReturnsFalseOnHeldKey(t *testing.T) {
	address, actions, stop := fakeServer(t, '#')
	defer stop()

	lc, err := NewLockingCenter(address)
	if err != nil {
		t.Fatalf("unable to connect: %s", err)
	}

	done := make(chan bool, 1)
	go func() { done <- lc.TryLock("held") }()

	select {
	case got := <-done:
		if got {
			t.Error("TryLock reported success for a held key")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("TryLock blocked on a held key instead of returning false")
	}

	select {
	case action := <-actions:
		if action != byte(maTryLock) {
			t.Errorf("TryLock sent action %d, expected the try-lock action %d", action, maTryLock)
		}
	case <-time.After(time.Second):
		t.Fatal("the server never received a request")
	}
}

func TestTryLockReturnsTrueOnFreeKey(t *testing.T) {
	address, _, stop := fakeServer(t, '+')
	defer stop()

	lc, err := NewLockingCenter(address)
	if err != nil {
		t.Fatalf("unable to connect: %s", err)
	}

	if !lc.TryLock("free") {
		t.Error("TryLock did not report success for a free key")
	}
}

// A failure answer '-' is also a false, the caller decides what to do.
func TestTryLockReturnsFalseOnFailure(t *testing.T) {
	address, _, stop := fakeServer(t, '-')
	defer stop()

	lc, err := NewLockingCenter(address)
	if err != nil {
		t.Fatalf("unable to connect: %s", err)
	}

	if lc.TryLock("whatever") {
		t.Error("TryLock reported success on a '-' answer")
	}
}
