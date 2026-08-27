package mutex

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"
)

type mutexAction byte

const (
	maLock          mutexAction = 1
	maUnlock        mutexAction = 2
	maResetByKey    mutexAction = 3
	maResetBySource mutexAction = 4
	maTryLock       mutexAction = 5
)

var queueRetryDuration = time.Millisecond * 500

// maxValueSize is the largest key or source address a request can carry. The
// server reads a size as a signed byte, so anything above 127 can never be sent
// and is a caller mistake, not a transient failure.
const maxValueSize = 127

// checkKey fails fast on a key that can never succeed, rather than letting the
// keep-trying loops spin on it forever.
func checkKey(key string) {
	if len(key) == 0 {
		panic("locking-center: key can not be empty")
	}
	if len(key) > maxValueSize {
		panic(fmt.Sprintf("locking-center: key can not be longer than %d bytes", maxValueSize))
	}
}

// LockingCenter is a client of a Locking-Center server. Every call opens its
// own short lived connection, so a value is safe to share between goroutines
// and there is nothing to close.
//
// A key is arbitrary bytes, 1 to 127 of them. The methods that take a key
// panic on an empty or over long one before any network activity, an invalid
// key is a programming error rather than a transient failure.
type LockingCenter interface {
	// Lock acquires the key, waiting in the server's queue until it is free.
	// It keeps trying through connection failures and returns only once the
	// key is held.
	Lock(key string)
	// TryLock acquires the key only if it is free right now and never waits.
	// It reports true when the key was acquired, and false when it is held by
	// somebody else or the server could not be reached, so the caller decides
	// what to do next.
	TryLock(key string) bool
	// Unlock releases the key. It keeps trying until the server confirms.
	Unlock(key string)
	// Wait blocks until the key is free and releases it again right away,
	// without holding it. It is a way to pause until the current holder is
	// done.
	Wait(key string)

	// ResetByKey force releases the key whoever holds it. It is the recovery
	// path for a key that a crashed client left locked.
	ResetByKey(key string)
	// ResetBySource force releases every key that the given owner holds. A
	// nil source lets the server fall back to the address of this
	// connection. It is the recovery path for a crashed instance, on
	// Kubernetes usually its pod IP.
	ResetBySource(sourceAddr *string)
}

type lockingCenter struct {
	address    *net.TCPAddr
	sourceAddr *string
}

// NewLockingCenter connects to the server at address ("host:port") and lets
// the server identify this owner by the address of each connection. It dials
// once to make sure the server is reachable and returns an error if it is not.
func NewLockingCenter(address string) (LockingCenter, error) {
	return NewLockingCenterWithSourceAddr(address, nil)
}

// NewLockingCenterWithSourceAddr is NewLockingCenter with an explicit source
// address that identifies this owner, so that ResetBySource can release
// everything it held after a crash. The source must be at most 127 bytes.
func NewLockingCenterWithSourceAddr(address string, sourceAddr *string) (LockingCenter, error) {
	addr, err := net.ResolveTCPAddr("tcp", address)
	if err != nil {
		return nil, err
	}

	if sourceAddr != nil && len(*sourceAddr) > maxValueSize {
		return nil, fmt.Errorf("source address can not be longer than %d bytes", maxValueSize)
	}

	lc := &lockingCenter{
		address:    addr,
		sourceAddr: sourceAddr,
	}
	if err := lc.ping(); err != nil {
		return nil, err
	}
	return lc, nil
}

func (l *lockingCenter) ping() error {
	conn, err := net.DialTCP("tcp", nil, l.address)
	if err != nil {
		return err
	}
	return conn.Close()
}

func (l *lockingCenter) preparePackage(action mutexAction, key string, sourceAddr *string) ([]byte, error) {
	data := make([]byte, 0)
	buffer := bytes.NewBuffer(data)

	if err := binary.Write(buffer, binary.LittleEndian, action); err != nil {
		return nil, err
	}

	switch action {
	case maLock, maTryLock, maUnlock, maResetByKey:
		keySize := int8(len(key))
		if err := binary.Write(buffer, binary.LittleEndian, keySize); err != nil {
			return nil, err
		}

		if err := binary.Write(buffer, binary.LittleEndian, []byte(key)); err != nil {
			return nil, err
		}
	}

	switch action {
	case maLock, maTryLock, maResetBySource:
		sourceAddrSize := int8(0)
		if sourceAddr != nil {
			sourceAddrSize = int8(len(*sourceAddr))
		}

		if err := binary.Write(buffer, binary.LittleEndian, sourceAddrSize); err != nil {
			return nil, err
		}

		if sourceAddr != nil {
			if err := binary.Write(buffer, binary.LittleEndian, []byte(*sourceAddr)); err != nil {
				return nil, err
			}
		}
	}

	return buffer.Bytes(), nil
}

func (l *lockingCenter) query(conn *net.TCPConn, action mutexAction, key string, sourceAddr *string) (int, error) {
	payload, err := l.preparePackage(action, key, sourceAddr)
	if err != nil {
		return -1, err
	}

	if _, err := conn.Write(payload); err != nil {
		return -1, err
	}

	res := l.result(conn)
	if res == -1 {
		return -1, fmt.Errorf("remote server execution error")
	}
	return res, nil
}

func (l *lockingCenter) result(conn *net.TCPConn) int {
	r := make([]byte, 1)

	if _, err := io.ReadAtLeast(conn, r, len(r)); err != nil {
		return -1
	}
	if string(r) != "+" {
		return 0
	}
	return 1
}

// Lock acquires the key and blocks until it is held. The server queues the
// request behind the current holder and answers only when the key is free, so
// the call may wait for as long as that holder keeps it. A connection failure
// or a '-' answer is retried every 500ms, the call returns only on success.
// It panics on an empty or over long key before any network activity.
func (l *lockingCenter) Lock(key string) {
	checkKey(key)

	query := func() bool {
		conn, err := net.DialTCP("tcp", nil, l.address)
		if err != nil {
			fmt.Printf("WARN: connection failure (keep trying): %s\n", err)
			return false
		}
		defer func() { _ = conn.Close() }()

		res, err := l.query(conn, maLock, key, l.sourceAddr)
		if err != nil {
			fmt.Printf("WARN: locking error (keep trying): %s\n", err)
			return false
		}
		return res == 1
	}

	for !query() {
		time.Sleep(queueRetryDuration)
	}
}

// TryLock attempts the lock once and returns immediately, unlike Lock which
// blocks until the key is free. It reports true when the key was acquired and
// false when it is held by somebody else, or the server could not be reached,
// so the caller decides whether to retry, wait or do something else.
func (l *lockingCenter) TryLock(key string) bool {
	checkKey(key)

	conn, err := net.DialTCP("tcp", nil, l.address)
	if err != nil {
		fmt.Printf("WARN: try locking connection failure: %s\n", err)
		return false
	}
	defer func() { _ = conn.Close() }()

	res, err := l.query(conn, maTryLock, key, l.sourceAddr)
	if err != nil {
		fmt.Printf("WARN: try locking error: %s\n", err)
		return false
	}
	return res == 1
}

// Unlock releases the key so the next queued request, if any, acquires it. It
// retries every 500ms until the server confirms. It panics on an empty or
// over long key before any network activity.
func (l *lockingCenter) Unlock(key string) {
	checkKey(key)

	query := func() bool {
		conn, err := net.DialTCP("tcp", nil, l.address)
		if err != nil {
			fmt.Printf("WARN: connection failure (keep trying): %s\n", err)
			return false
		}
		defer func() { _ = conn.Close() }()

		res, err := l.query(conn, maUnlock, key, nil)
		if err != nil {
			fmt.Printf("WARN: unlocking error (keep trying): %s\n", err)
			return false
		}
		return res == 1
	}

	for !query() {
		time.Sleep(queueRetryDuration)
	}
}

// Wait blocks until the key is free and then releases it right away, without
// keeping it. It is Lock followed by Unlock: a way to pause until whoever holds
// the key is done, when there is no work of your own to protect.
func (l *lockingCenter) Wait(key string) {
	l.Lock(key)
	defer l.Unlock(key)
}

// ResetByKey force releases the key no matter who holds it and lets the queued
// requests contend for it again. A lock is not tied to its connection, so a
// client that crashes while holding a key leaves it locked; this is how an
// operator or a supervisor clears such a stuck lock. It retries every 500ms
// until the server confirms and panics on an empty or over long key.
func (l *lockingCenter) ResetByKey(key string) {
	checkKey(key)

	query := func() bool {
		conn, err := net.DialTCP("tcp", nil, l.address)
		if err != nil {
			fmt.Printf("WARN: connection failure (keep trying): %s\n", err)
			return false
		}
		defer func() { _ = conn.Close() }()

		res, err := l.query(conn, maResetByKey, key, nil)
		if err != nil {
			fmt.Printf("WARN: resetting error (keep trying): %s\n", err)
			return false
		}
		return res == 1
	}

	for !query() {
		time.Sleep(queueRetryDuration)
	}
}

// ResetBySource force releases every key held by the owner identified by
// sourceAddr, the address a client was constructed with. It is the recovery
// path for a whole instance that went away, on Kubernetes typically a crashed
// pod's IP. A nil sourceAddr lets the server fall back to the address of this
// connection. It retries every 500ms until the server confirms and panics on
// a source longer than 127 bytes.
func (l *lockingCenter) ResetBySource(sourceAddr *string) {
	if sourceAddr != nil && len(*sourceAddr) > maxValueSize {
		panic(fmt.Sprintf("locking-center: source address can not be longer than %d bytes", maxValueSize))
	}

	query := func() bool {
		conn, err := net.DialTCP("tcp", nil, l.address)
		if err != nil {
			fmt.Printf("WARN: connection failure (keep trying): %s\n", err)
			return false
		}
		defer func() { _ = conn.Close() }()

		res, err := l.query(conn, maResetBySource, "", sourceAddr)
		if err != nil {
			fmt.Printf("WARN: reseting error (keep trying): %s\n", err)
			return false
		}
		return res == 1
	}

	for !query() {
		time.Sleep(queueRetryDuration)
	}
}
