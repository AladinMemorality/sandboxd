package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/workerstop"
	"os"
	"time"
)

func main() {
	observe := flag.Bool("observe", false, "read-only binding/provider/admission observation")
	flag.Parse()
	if flag.NArg() != 0 {
		fail()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	c, e := workerstop.LoadConfig(workerstop.ConfigPath)
	if e != nil {
		fail()
	}
	if *observe {
		out, e := workerstop.Observe(ctx, c)
		if e != nil {
			fail()
		}
		if json.NewEncoder(os.Stdout).Encode(out) != nil {
			os.Exit(1)
		}
		return
	}
	sc, e := workerstop.LoadStartConfig()
	if e != nil {
		fail()
	}
	out, e := workerstop.ReconcileStart(ctx, c, sc)
	if e != nil {
		fail()
	}
	if json.NewEncoder(os.Stdout).Encode(out) != nil {
		os.Exit(1)
	}
}
func fail() {
	fmt.Fprintln(os.Stderr, "offline worker startup reconciliation refused; controller remains gated unless exact marker removal completed")
	os.Exit(1)
}
