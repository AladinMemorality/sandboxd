// Offline recovery ZIP validation using the actual runtimed import contracts.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"

	rt "github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
)

func regular(name string, limit int64) (*os.File, int64, error) {
	fd, e := syscall.Open(name, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if e != nil {
		return nil, 0, e
	}
	f := os.NewFile(uintptr(fd), name)
	s, e := f.Stat()
	if e != nil || !s.Mode().IsRegular() || s.Size() > limit {
		f.Close()
		return nil, 0, errors.New("invalid bounded archive")
	}
	return f, s.Size(), nil
}
func check() error {
	if len(os.Args) != 4 {
		return errors.New("usage: recovery-archive-check APP_ZIP HOME_ZIP HOME_MANIFEST")
	}
	app, appSize, e := regular(os.Args[1], rt.MaxPrivateWorkspaceStreamBytes)
	if e != nil {
		return e
	}
	defer app.Close()
	home, homeSize, e := regular(os.Args[2], rt.MaxPrivateHomeBytes)
	if e != nil {
		return e
	}
	defer home.Close()
	file, _, e := regular(os.Args[3], rt.MaxHomeManifestV2Bytes)
	if e != nil {
		return e
	}
	defer file.Close()
	b, e := io.ReadAll(file)
	if e != nil {
		return e
	}
	var manifest rt.HomeManifest
	if json.Unmarshal(b, &manifest) != nil {
		return errors.New("invalid home manifest")
	}
	appDigest, e := rt.PrivateWorkspaceFileDigest(app, appSize)
	if e != nil {
		return e
	}
	homeDigest, e := rt.PrivateHomeDigest(manifest, home, homeSize)
	if e != nil {
		return e
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"valid": true, "app_tree_digest": appDigest, "home_tree_digest": homeDigest, "runtimed_contract": "private-workspace-v2+private-home-v2", "postgres_recovery_verified": false})
}
func main() {
	if e := check(); e != nil {
		fmt.Fprintln(os.Stderr, "recovery archive rejected by existing runtimed import contract")
		os.Exit(1)
	}
}
