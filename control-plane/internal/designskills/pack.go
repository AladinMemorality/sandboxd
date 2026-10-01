// Package designskills owns the design resources shared by image builds and
// task dispatch. Delivery at dispatch also covers old, imported and remixed VMs.
package designskills

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sync"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
)

//go:embed pack
var source embed.FS

type File struct {
	Path    string
	Content []byte
}

func Files() []File {
	var files []File
	// WalkDir is sorted, so the digest is reproducible across builds.
	fs.WalkDir(source, "pack", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			panic(err)
		}
		if !entry.IsDir() {
			data, err := source.ReadFile(path)
			if err != nil {
				panic(err)
			}
			files = append(files, File{path[len("pack/"):], data})
		}
		return nil
	})
	return files
}

func Directory() string {
	h := sha256.New()
	for _, f := range Files() {
		fmt.Fprintf(h, "%s\x00%d\x00", f.Path, len(f.Content))
		h.Write(f.Content)
	}
	return fmt.Sprintf(".baarcha/design-skills/%x", h.Sum(nil))
}

// Prompt is a navigation aid rather than a copy of the skill bodies. The
// original user request remains intact and stored separately in task history.
func Prompt() string {
	return "Baarcha shared design skills are available in this workspace at " + Directory() + "/. For visual work, read GUIDE.md there and the relevant SKILL.md before implementation, implement the design and send ready-screen reports to Hannibal for visual review. Hannibal owns live-preview screenshots; do not capture them yourself or wait for review. This replaces legacy self-screenshot capture instructions. This versioned pack is the platform's current design guidance; preserve custom project instructions and the user's design choices. For nonvisual work, no design detour is needed.\n\n"
}

// Ensure verifies actual file contents, not a marker that could outlive deleted
// files. It never writes into user-maintained .claude/skills or AGENTS.md.
// The guest file API refuses symlinks and writes atomically. A failed delivery
// aborts before a task is accepted, and a retry safely repairs partial delivery.
func Ensure(ctx context.Context, client *runtime.Client) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	files, dir := Files(), Directory()
	jobs := make(chan File)
	var wg sync.WaitGroup
	var once sync.Once
	var failure error
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for f := range jobs {
				path := dir + "/" + f.Path
				got, err := client.ReadFile(ctx, path)
				var response *runtime.ResponseError
				if err == nil && bytes.Equal(got, f.Content) {
					continue
				}
				if err == nil || (errors.As(err, &response) && response.StatusCode == 404) {
					var out *runtime.FileWrite
					out, err = client.PutFile(ctx, path, bytes.NewReader(f.Content))
					if err == nil && (out.Path != path || out.Size != int64(len(f.Content))) {
						err = errors.New("invalid skill write acknowledgement")
					}
				}
				if err != nil {
					once.Do(func() { failure = fmt.Errorf("design skill %s: %w", f.Path, err); cancel() })
				}
			}
		}()
	}
send:
	for _, f := range files {
		select {
		case jobs <- f:
		case <-ctx.Done():
			break send
		}
	}
	close(jobs)
	wg.Wait()
	if failure != nil {
		return failure
	}
	return ctx.Err()
}
