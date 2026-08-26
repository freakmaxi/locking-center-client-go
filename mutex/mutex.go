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
)

var queueRetryDuration = time.Millisecond * 500

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
	if action != maResetBySource && len(key) == 0 || len(key) > 128 {
		return nil, fmt.Errorf("key can not be empty or more than 128 characters")
	}

	data := make([]byte, 0)
	buffer := bytes.NewBuffer(data)

	if err := binary.Write(buffer, binary.LittleEndian, action); err != nil {
		return nil, err
	}

	switch action {
	case maLock, maUnlock, maResetByKey:
		keySize := int8(len(key))
		if err := binary.Write(buffer, binary.LittleEndian, keySize); err != nil {
			return nil, err
		}

		if err := binary.Write(buffer, binary.LittleEndian, []byte(key)); err != nil {
			return nil, err
		}
	}

	switch action {
	case maLock, maResetBySource:
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

func (l *lockingCenter) TryLock(key string) bool {
	query := func() int {
		conn, err := net.DialTCP("tcp", nil, l.address)
		if err != nil {
			fmt.Printf("WARN: connection failure (keep trying): %s\n", err)
			return -1
		}
		defer func() { _ = conn.Close() }()

		res, err := l.query(conn, maLock, key, l.sourceAddr)
		if err != nil {
			fmt.Printf("WARN: try locking error (keep trying): %s\n", err)
		}
		return res
	}

	for {
		res := query()
		if res == -1 {
			time.Sleep(queueRetryDuration)
			continue
		}
		return res == 1
	}
}

func (l *lockingCenter) Unlock(key string) {
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
