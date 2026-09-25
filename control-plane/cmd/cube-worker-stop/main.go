package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/workerstop"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	path := flag.String("config", workerstop.ConfigPath, "fixed reviewed private config")
	inventory := flag.Bool("inventory", false, "offline pre-drain inventory only; no guest mutation")
	flag.Parse()
	if *path != workerstop.ConfigPath {
		fmt.Fprintln(os.Stderr, "fixed operator configuration required")
		os.Exit(1)
	}
	config, e := workerstop.LoadConfig(*path)
	if e != nil {
		fmt.Fprintln(os.Stderr, "private stop configuration refused")
		os.Exit(1)
	}
	if *inventory {
		value, e := workerstop.ReadInventory(context.Background(), config)
		if e != nil {
			fmt.Fprintln(os.Stderr, "offline inventory refused")
			os.Exit(1)
		}
		_ = json.NewEncoder(os.Stdout).Encode(value)
		return
	}
	// Ordinary TERM/INT cannot drop the controller lock while QEMU remains alive.
	holdSignals()
	held, e := workerstop.Open(context.Background(), config)
	if e != nil {
		fmt.Println(`{"version":1,"verified":false,"error":"preflight-blocked"}`)
		if held != nil {
			held.HoldUntilQEMUExit()
		}
		os.Exit(1)
	}
	proof, e := held.Prepare(context.Background())
	if e != nil {
		fmt.Println(`{"version":1,"verified":false,"error":"pause-or-proof-blocked"}`)
	} else {
		_ = json.NewEncoder(os.Stdout).Encode(proof)
	}
	// Never interpret stdin close as authorization to release locks or restart.
	held.HoldUntilQEMUExit()
	if e != nil {
		os.Exit(1)
	}
}

func holdSignals() { signal.Ignore(syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP, syscall.SIGPIPE) }
