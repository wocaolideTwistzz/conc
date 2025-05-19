# conc (Fork)

This repository is a fork of [sourcegraph/conc](https://github.com/sourcegraph/conc).

## Modifications

- Added a new feature: **Racer**

### Racer

Racer is a concurrent execution pattern that runs multiple operations simultaneously and returns the fastest result currently executed when you call **Next()**.

#### Use Case: Fast TCP Dialing

In Go, the default [TCP dial](https://github.com/golang/go/blob/master/src/net/dial.go#L632) is sequential.

Using Racer, you can attempt multiple connections simultaneously and get the fastest successful connection.  

Here's an example of using Racer for faster TCP connections:

```go
package main

import (
	"context"
	"log"
	"net"
	"time"

	"github.com/wocaolideTwistzz/conc/racer"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*15)
	defer cancel()

	var (
		domain   = "www.bilibili.com"
		ips, err = net.LookupIP(domain)
		dialer   net.Dialer
	)
	if err != nil || len(ips) == 0 {
		log.Fatalf("domain: %s resolve failed: %v", domain, err)
	}

	taskBuilder := func(ip net.IP) racer.Task[net.Conn] {
		return func(ctx context.Context) (net.Conn, error) {
			addr := net.TCPAddr{
				IP:   ip,
				Port: 443,
			}
			return dialer.DialContext(ctx, "tcp", addr.String())
		}
	}

	var tasks = make([]racer.Task[net.Conn], 0, len(ips))
	for _, ip := range ips {
		tasks = append(tasks, taskBuilder(ip))
	}
	racer := racer.New(ctx, tasks[0], tasks[1:]...).
		// Fallbacks trigger when primary is not completed whin 200ms
		WithTimeWait(time.Millisecond * 200).
		WithMaxGoroutines(3).
		// Close the connection when it is not used
		WithOnClose(func(c net.Conn) {
			c.Close()
		})
	// Close racer
	defer racer.Close()

	conn, err := racer.Next()
	if err != nil {
		log.Fatalf("all connections failed: %v", err)
	}
	// Use the fastest connection
	// ...
}
```

