# Locking-Center Go Client

The Go Connector of Locking-Center that is a mutex point to synchronize access between different services. You can limit the 
execution between services and create queueing for the operation.

- [Locking-Center Server](https://github.com/freakmaxi/locking-center)

#### Installation

`go get github.com/freakmaxi/locking-center-client-go/mutex`

#### Usage

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
	fmt.Println("Hello from locked area!")
	m.Unlock("locking-key")
}
```

`Lock` blocks until the key is free. `TryLock` is the non blocking form: it takes the key only if it is free right now
and returns immediately, so you decide what to do when somebody else holds it.

```go
if m.TryLock("locking-key") {
	defer m.Unlock("locking-key")
	fmt.Println("Got the lock, doing the work.")
} else {
	fmt.Println("Someone else holds it, skipping.")
}
```