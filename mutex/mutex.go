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
	Unlock(key string)
	Wait(key string)

	ResetByKey(key string)
	ResetBySource(sourceAddr string)
}

type lockingCenter struct {
	address *net.TCPAddr
}

func NewLockingCenter(address string) (LockingCenter, error) {
	addr, err := net.ResolveTCPAddr("tcp", address)
	if err != nil {
		return nil, err
	}

	lc := &lockingCenter{
		address: addr,
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

func (l *lockingCenter) preparePackage(key string, emptyAllowed bool, action mutexAction) ([]byte, error) {
	if (!emptyAllowed && len(key) == 0) || len(key) > 128 {
		return nil, fmt.Errorf("key can not be empty or more than 128 characters")
	}

	data := make([]byte, 0)
	buffer := bytes.NewBuffer(data)

	keySize := int8(len(key))
	if err := binary.Write(buffer, binary.LittleEndian, keySize); err != nil {
		return nil, err
	}

	if err := binary.Write(buffer, binary.LittleEndian, []byte(key)); err != nil {
		return nil, err
	}

	if err := binary.Write(buffer, binary.LittleEndian, action); err != nil {
		return nil, err
	}

	return buffer.Bytes(), nil
}

func (l *lockingCenter) query(conn *net.TCPConn, key string, emptyAllowed bool, action mutexAction) error {
	payload, err := l.preparePackage(key, emptyAllowed, action)
	if err != nil {
		return err
	}

	if _, err := conn.Write(payload); err != nil {
		return err
	}

	if !l.result(conn) {
		return fmt.Errorf("remote server execution error")
	}

	return nil
}

func (l *lockingCenter) result(conn *net.TCPConn) bool {
	r := make([]byte, 1)

	if _, err := io.ReadAtLeast(conn, r, len(r)); err != nil {
		return false
	}

	return string(r) == "+"
}

func (l *lockingCenter) Lock(key string) {
	query := func() bool {
		conn, err := net.DialTCP("tcp", nil, l.address)
		if err != nil {
			fmt.Printf("ERROR: connection failure: %s\n", err)
			return false
		}
		defer func() { _ = conn.Close() }()

		if err := l.query(conn, key, false, maLock); err != nil {
			fmt.Printf("ERROR: locking error: %s\n", err)
			return false
		}

		return true
	}

	for !query() {
		time.Sleep(queueRetryDuration)
	}
}

func (l *lockingCenter) Unlock(key string) {
	query := func() bool {
		conn, err := net.DialTCP("tcp", nil, l.address)
		if err != nil {
			fmt.Printf("ERROR: connection failure: %s\n", err)
			return false
		}
		defer func() { _ = conn.Close() }()

		if err := l.query(conn, key, false, maUnlock); err != nil {
			fmt.Printf("ERROR: unlocking error: %s\n", err)
			return false
		}

		return true
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
			fmt.Printf("ERROR: connection failure: %s\n", err)
			return false
		}
		defer func() { _ = conn.Close() }()

		if err := l.query(conn, key, false, maResetByKey); err != nil {
			fmt.Printf("ERROR: reseting error: %s\n", err)
			return false
		}

		return true
	}

	for !query() {
		time.Sleep(queueRetryDuration)
	}
}

func (l *lockingCenter) ResetBySource(sourceAddr string) {
	query := func() bool {
		conn, err := net.DialTCP("tcp", nil, l.address)
		if err != nil {
			fmt.Printf("ERROR: connection failure: %s\n", err)
			return false
		}
		defer func() { _ = conn.Close() }()

		if err := l.query(conn, sourceAddr, true, maResetBySource); err != nil {
			fmt.Printf("ERROR: reseting error: %s\n", err)
			return false
		}

		return true
	}

	for !query() {
		time.Sleep(queueRetryDuration)
	}
}
