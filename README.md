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