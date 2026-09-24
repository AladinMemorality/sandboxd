package main

import (
	rt "github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"os"
)

func fileSize(f *os.File) int64 { s, e := f.Stat(); require(e); return s.Size() }
func appDigest(f *os.File) string {
	d, e := rt.PrivateWorkspaceFileDigest(f, fileSize(f))
	require(e)
	return d
}
func homeDigest(m rt.HomeManifest, f *os.File) string {
	d, e := rt.PrivateHomeDigest(m, f, fileSize(f))
	require(e)
	return d
}
