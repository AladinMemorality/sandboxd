// This operator-only relay runs in the hypervisor container's network namespace.
// SSH authenticates the guest end to end; no private keys enter this process.
package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"time"
)

func main() {
	conn, err := net.DialTimeout("tcp", "127.0.0.1:24222", 10*time.Second)
	if err != nil {
		fmt.Fprintln(os.Stderr, "worker SSH listener unavailable")
		os.Exit(1)
	}
	defer conn.Close()
	go func() {
		_, _ = io.Copy(conn, os.Stdin)
		_ = conn.(*net.TCPConn).CloseWrite()
	}()
	if _, err = io.Copy(os.Stdout, conn); err != nil {
		fmt.Fprintln(os.Stderr, "worker SSH transport interrupted")
		os.Exit(1)
	}
}
