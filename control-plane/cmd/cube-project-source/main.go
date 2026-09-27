// cube-project-source packages an already quiesced owner export. It does not
// contact a guest, run user code, upload data or change a runtime binding.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/projectsource"
	"io"
	"os"
	"path/filepath"
)

func read(p string, max int64) ([]byte, error) {
	f, e := os.Open(p)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, max+1))
	if e == nil && int64(len(b)) > max {
		return nil, errors.New("input too large")
	}
	return b, e
}
func run() error {
	if len(os.Args) != 4 {
		return errors.New("usage: cube-project-source INPUT.zip RECIPE.json NEW_OUTPUT_DIRECTORY")
	}
	raw, e := read(os.Args[2], 1<<20)
	if e != nil {
		return e
	}
	var recipe projectsource.Recipe
	if e = json.Unmarshal(raw, &recipe); e != nil {
		return e
	}
	input, e := read(os.Args[1], projectsource.MaxBytes)
	if e != nil {
		return e
	}
	source, manifest, e := projectsource.Build(input, recipe)
	if e != nil {
		return e
	}
	if e = projectsource.Verify(source, manifest); e != nil {
		return e
	}
	if e = os.Mkdir(os.Args[3], 0700); e != nil {
		return e
	}
	for _, file := range []struct {
		name string
		data []byte
	}{{"source.zip", source}, {"manifest.json", mustJSON(manifest)}} {
		f, e := os.OpenFile(filepath.Join(os.Args[3], file.name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return e
		}
		_, e = f.Write(file.data)
		if e == nil {
			e = f.Sync()
		}
		ce := f.Close()
		if e != nil {
			return e
		}
		if ce != nil {
			return ce
		}
	}
	dir, e := os.Open(os.Args[3])
	if e != nil {
		return e
	}
	defer dir.Close()
	if e = dir.Sync(); e != nil {
		return e
	}
	fmt.Printf("source_sha256=%s bytes=%d files=%d excluded=%d\n", manifest.SourceSHA256, manifest.Bytes, len(manifest.Files), len(manifest.Excluded))
	return nil
}
func mustJSON(v any) []byte {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		panic(e)
	}
	return append(b, '\n')
}
func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
