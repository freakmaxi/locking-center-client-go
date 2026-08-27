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

type LockingCenter interface {
	Lock(key string)
	TryLock(key string) bool
	Unlock(key string)
	Wait(key string)

	ResetByKey(key string)
	ResetBySource(sourceAddr *string)
}

type lockingCenter struct {
	address    *net.TCPAddr
	sourceAddr *string
}

func NewLockingCenter(address string) (LockingCenter, error) {
	return NewLockingCenterWithSourceAddr(address, nil)
}

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

func (l *lockingCenter) Wait(key string) {
	l.Lock(key)
	defer l.Unlock(key)
}

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
