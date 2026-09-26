// cube-pair-verify reads a restored pair. It never starts a controller or mutates a guest.
package main

import (
	"context"
	"fmt"
	"os"
	"time"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: cube-pair-verify ROOT_PRIVATE_CONFIG")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	if err := run(ctx, os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, "Restored-pair verification refused; no mutation or retry performed.")
		os.Exit(1)
	}
}
