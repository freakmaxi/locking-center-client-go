# Locking-Center Go Client

The Go connector for [Locking-Center](https://github.com/freakmaxi/locking-center), a mutex point that synchronizes
access to shared resources between different services. Lock a key before you touch the resource, do the work, unlock the
key. Only one caller holds a given key at a time, the rest queue up and are served in order.

- [Locking-Center Server](https://github.com/freakmaxi/locking-center)

## Installation

```shell
go get github.com/freakmaxi/locking-center-client-go/mutex
```

## Quick start

```go
package main

import (
	"fmt"

	"github.com/freakmaxi/locking-center-client-go/mutex"
)

func main() {
	m, err := mutex.NewLockingCenter("localhost:22119")
	if err != nil {
		panic(err)
	}

	m.Lock("locking-key")
	defer m.Unlock("locking-key")

	fmt.Println("Hello from the locked area!")
}
```

## Connecting

```go
// simplest form
m, err := mutex.NewLockingCenter("localhost:22119")

// with a source address, which identifies this owner for crash recovery, see below
source := "10.0.0.4"
m, err := mutex.NewLockingCenterWithSourceAddr("localhost:22119", &source)
```

The constructor dials the server once to make sure it is reachable and returns an error if it is not. The returned
value is safe to keep and share across goroutines; every call opens its own short-lived connection.

## API

| Method | Blocks | Description |
| --- | --- | --- |
| `Lock(key)` | yes | Acquires the key, waiting in the queue until it is free |
| `TryLock(key) bool` | no | Acquires the key only if it is free right now, returns whether it did |
| `Unlock(key)` | no | Releases the key |
| `Wait(key)` | yes | Waits for the key to be free, then releases it again without holding it |
| `ResetByKey(key)` | no | Force releases a key, whoever holds it (crash recovery) |
| `ResetBySource(sourceAddr)` | no | Force releases everything a given owner held (crash recovery) |

### Locking

`Lock` blocks until the key is free, then takes it. It keeps trying through connection failures, so it returns only
once the key is held.

```go
m.Lock("orders/batch-7")
defer m.Unlock("orders/batch-7")
// ... exclusive work ...
```

### Try locking

`TryLock` is the non-blocking form. It takes the key only if it is free at that moment and returns immediately, so you
decide what to do when somebody else holds it.

```go
if m.TryLock("orders/batch-7") {
	defer m.Unlock("orders/batch-7")
	// ... exclusive work ...
} else {
	// someone else holds it, skip, retry later, or do something else
}
```

`TryLock` returns `false` when the key is held by another owner **and** when the server cannot be reached, so a `false`
means only "you did not get the lock". If you need to tell the two apart, check reachability separately.

### Waiting

`Wait` blocks until the key is free and then releases it immediately, without holding it. Use it to pause until whoever
holds the key is done.

```go
m.Wait("migration-done") // returns once the key is free
```

## Crash recovery: reset

A lock is not tied to its TCP connection, so a client that crashes while holding a key leaves that key locked. Nothing
releases it automatically. `Reset` is how an operator or a supervisor clears such a stuck lock.

```go
m.ResetByKey("orders/batch-7") // release this key, whoever holds it

crashed := "10.0.0.9"
m.ResetBySource(&crashed)      // release everything 10.0.0.9 held
```

`ResetBySource` matches on the **source address**. Pass the source when you construct the client
(`NewLockingCenterWithSourceAddr`) so that each owner is identifiable; on Kubernetes, pass the pod IP. A `nil` source
lets the server fall back to the connection's peer address.

## Keys

A key must be **between 1 and 127 bytes**. An empty or over-long key is a programming error, so the client **panics**
right away instead of hanging in the retry loop. Keep keys within that range, they are arbitrary bytes otherwise.

## Behaviour to know

- **`Lock`, `Unlock` and the resets keep retrying until they succeed.** They do not return an error; a server that is
down just means the call keeps trying (with a short delay between attempts). Wrap a call in your own goroutine with a
timeout if you need to give up.
- **Do not put a read timeout on the connection for `Lock`.** The server holds the connection open for as long as the
key is held by its current owner, which is unbounded. The client already accounts for this.
- **Every call is one short-lived TCP connection.** There is no pool to manage and nothing to close.
- **The client is safe for concurrent use** from many goroutines.

## License

[Apache License 2.0](LICENSE). The Locking-Center server itself is licensed separately under the GPL-3.0; the
clients are permissive so they can be embedded in any service.
