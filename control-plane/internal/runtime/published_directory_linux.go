package runtime

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"os"
	"path"

	"golang.org/x/sys/unix"
)

// ExportPublishedDirectory converts an immutable, owner-scoped legacy app
// snapshot into the same public source format as a Cube publication. Never
// export the snapshot's home directory or use a private migration archive here.
// Every opened path is descriptor-relative and refuses links and mount changes.
func ExportPublishedDirectory(ctx context.Context, root string) ([]byte, error) {
	dir, err := privateDirectory(root)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	var rootStat unix.Stat_t
	if err = unix.Fstat(int(dir.Fd()), &rootStat); err != nil {
		return nil, err
	}
	var out archiveBuffer
	zw := zip.NewWriter(&out)
	seen, count := 0, 0
	var total int64
	var walk func(*os.File, string) error
	walk = func(parent *os.File, prefix string) error {
		for {
			entries, readErr := parent.ReadDir(128)
			if readErr != nil && readErr != io.EOF {
				return readErr
			}
			for _, entry := range entries {
				if err := ctx.Err(); err != nil {
					return err
				}
				seen++
				name := path.Join(prefix, entry.Name())
				if seen > MaxWorkspaceEntries || !ValidArchivePath(name) {
					return errors.New("source directory exceeds path or entry limit")
				}
				isDir := entry.IsDir()
				if isDir && !PublishedSourcePath(name+"/source.js") {
					continue
				}
				if !isDir && !PublishedSourcePath(name) {
					continue
				}
				child, err := privateChild(parent, entry.Name(), isDir)
				if err != nil {
					return errors.New("source contains an unsafe path")
				}
				err = func() error {
					defer child.Close()
					var stat unix.Stat_t
					if err := unix.Fstat(int(child.Fd()), &stat); err != nil {
						return err
					}
					if stat.Dev != rootStat.Dev {
						return errors.New("source crosses a filesystem boundary")
					}
					if isDir {
						return walk(child, name)
					}
					if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Nlink != 1 {
						return errors.New("source contains a linked or special file")
					}
					if stat.Size < 0 || stat.Size > MaxFileWriteBytes || total+stat.Size > MaxWorkspaceExportBytes {
						return errors.New("source exceeds byte limit")
					}
					header := &zip.FileHeader{Name: name, Method: zip.Deflate}
					header.SetMode(0644)
					writer, err := zw.CreateHeader(header)
					if err != nil {
						return err
					}
					n, err := io.Copy(writer, io.LimitReader(child, stat.Size+1))
					if err != nil {
						return err
					}
					if n != stat.Size {
						return errors.New("source changed during export")
					}
					total += n
					count++
					return nil
				}()
				if err != nil {
					return err
				}
			}
			if readErr == io.EOF {
				return nil
			}
		}
	}
	if err = walk(dir, ""); err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, errors.New("source has no publishable files")
	}
	if err = zw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
