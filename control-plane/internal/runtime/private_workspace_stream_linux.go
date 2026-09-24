package runtime

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

const MaxPrivateWorkspaceStreamBytes int64 = 4 << 30
const MaxPrivateWorkspaceStreamExpandedBytes int64 = 8 << 30
const MaxPrivateWorkspaceStreamFileBytes int64 = 1 << 30

// ValidPrivateWorkspaceLink shares the private transport policy with offline
// migration preflight; destination interpreter compatibility is checked on import.
func ValidPrivateWorkspaceLink(name, target string) bool { return privateLink(name, target) }

type workspaceArchiveLimits struct {
	compressed, expanded, file int64
	entries                    int
}

var workspaceV1Limits = workspaceArchiveLimits{MaxPrivateWorkspaceBytes, MaxPrivateWorkspaceExpandedBytes, MaxPrivateWorkspaceFileBytes, MaxPrivateWorkspaceEntries}
var workspaceStreamLimits = workspaceArchiveLimits{MaxPrivateWorkspaceStreamBytes, MaxPrivateWorkspaceStreamExpandedBytes, MaxPrivateWorkspaceStreamFileBytes, MaxPrivateWorkspaceEntries}

type workspaceContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r workspaceContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

type workspaceContextReaderAt struct {
	ctx    context.Context
	reader io.ReaderAt
}

func (r workspaceContextReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.ReadAt(p, off)
}

type workspaceBoundedWriter struct {
	writer       io.Writer
	max, written int64
}

func (w *workspaceBoundedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.max-w.written {
		return 0, errors.New("compressed workspace limit")
	}
	n, err := w.writer.Write(p)
	w.written += int64(n)
	return n, err
}

// ExportPrivateWorkspaceFile writes a bounded private ZIP directly to an
// operator-owned recovery file. It never buffers a whole archive or file. All
// owner writes must remain fenced until the journal commits the provider switch.
func ExportPrivateWorkspaceFile(ctx context.Context, root, ownerHome string, dest io.Writer) error {
	rel, err := filepath.Rel(ownerHome, root)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") || filepath.IsAbs(rel) {
		return errors.New("workspace outside owner home")
	}
	var links map[privateInode]uint64
	return exportPrivateWorkspaceTo(ctx, root, dest, workspaceStreamLimits, func() (map[privateInode]uint64, error) {
		if links == nil {
			var err error
			links, err = inventoryOwnerLinks(ctx, ownerHome)
			if err != nil {
				return nil, err
			}
		}
		return links, nil
	})
}

func exportPrivateWorkspaceTo(ctx context.Context, root string, dest io.Writer, limits workspaceArchiveLimits, ownerLinks func() (map[privateInode]uint64, error)) error {
	dir, err := privateDirectory(root)
	if err != nil {
		return err
	}
	defer dir.Close()
	var rootMount unix.Statx_t
	if err = unix.Statx(int(dir.Fd()), "", unix.AT_EMPTY_PATH, unix.STATX_MNT_ID, &rootMount); err != nil || rootMount.Mask&unix.STATX_MNT_ID == 0 {
		return errors.New("workspace mount identity unavailable")
	}
	z := zip.NewWriter(&workspaceBoundedWriter{writer: dest, max: limits.compressed})
	count := 0
	var size, indexSize int64
	var walk func(*os.File, string, int) error
	walk = func(parent *os.File, prefix string, depth int) error {
		if depth > 32 {
			return errors.New("workspace depth exceeds limit")
		}
		for {
			names, readErr := parent.Readdirnames(128)
			if readErr != nil && readErr != io.EOF {
				return readErr
			}
			for _, name := range names {
				if err := ctx.Err(); err != nil {
					return err
				}
				full := path.Join(prefix, name)
				count++
				if !ValidArchivePath(full) || len(full) > 4096 || count > limits.entries {
					return errors.New("workspace path or entry limit")
				}
				indexSize += int64(256 + len(full))
				if indexSize > 64<<20 {
					return errors.New("workspace index limit")
				}
				var st unix.Stat_t
				if err := unix.Fstatat(int(parent.Fd()), name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
					return err
				}
				var mount unix.Statx_t
				if err := unix.Statx(int(parent.Fd()), name, unix.AT_SYMLINK_NOFOLLOW, unix.STATX_MNT_ID, &mount); err != nil || mount.Mask&unix.STATX_MNT_ID == 0 || mount.Mnt_id != rootMount.Mnt_id {
					return errors.New("workspace crosses mount boundary")
				}
				h := &zip.FileHeader{Name: full, Method: zip.Deflate}
				h.SetMode(os.FileMode(st.Mode & 0777))
				switch st.Mode & unix.S_IFMT {
				case unix.S_IFDIR:
					h.Name += "/"
					h.SetMode(os.ModeDir | os.FileMode(st.Mode&0777))
					if _, err := z.CreateHeader(h); err != nil {
						return err
					}
					child, err := privateChild(parent, name, true)
					if err != nil {
						return err
					}
					err = walk(child, full, depth+1)
					child.Close()
					if err != nil {
						return err
					}
				case unix.S_IFLNK:
					buf := make([]byte, 4097)
					n, err := unix.Readlinkat(int(parent.Fd()), name, buf)
					if err != nil {
						return err
					}
					if n > 4096 || !privateLink(full, string(buf[:n])) {
						return errors.New("workspace symlink escapes root")
					}
					if int64(n) > limits.expanded-size {
						return errors.New("expanded workspace limit")
					}
					size += int64(n)
					h.SetMode(os.ModeSymlink | 0777)
					w, err := z.CreateHeader(h)
					if err != nil {
						return err
					}
					if _, err = w.Write(buf[:n]); err != nil {
						return err
					}
				case unix.S_IFREG:
					err := func() error {
						f, err := privateChild(parent, name, false)
						if err != nil {
							return err
						}
						defer f.Close()
						var actual unix.Stat_t
						if err = unix.Fstat(int(f.Fd()), &actual); err != nil {
							return err
						}
						if actual.Mode&unix.S_IFMT != unix.S_IFREG || actual.Size < 0 || actual.Size > limits.file || actual.Size > limits.expanded-size {
							return errors.New("invalid or oversized workspace file")
						}
						if actual.Nlink != 1 {
							links, err := ownerLinks()
							if err != nil {
								return err
							}
							if actual.Uid != 1000 || links[privateInode{uint64(actual.Dev), actual.Ino}] != uint64(actual.Nlink) {
								return errPrivateWorkspaceHardlink
							}
						}
						h.SetMode(os.FileMode(actual.Mode & 0777))
						w, err := z.CreateHeader(h)
						if err != nil {
							return err
						}
						n, err := io.Copy(w, io.LimitReader(workspaceContextReader{ctx: ctx, reader: f}, actual.Size+1))
						if err != nil {
							return err
						}
						if n != actual.Size {
							return errors.New("workspace changed during export")
						}
						size += n
						return nil
					}()
					if err != nil {
						return err
					}
				default:
					return errors.New("workspace contains special file")
				}
			}
			if readErr == io.EOF {
				return nil
			}
		}
	}
	if err = walk(dir, "", 0); err != nil {
		return err
	}
	return z.Close()
}

func PrivateWorkspaceFileDigest(reader io.ReaderAt, size int64) (string, error) {
	return PrivateWorkspaceFileDigestContext(context.Background(), reader, size)
}
func PrivateWorkspaceFileDigestContext(ctx context.Context, reader io.ReaderAt, size int64) (string, error) {
	files, err := privateWorkspaceReader(workspaceContextReaderAt{ctx: ctx, reader: reader}, size, workspaceStreamLimits)
	if err != nil {
		return "", err
	}
	return privateWorkspaceDigestEntries(files)
}
func ValidatePrivateWorkspaceFile(reader io.ReaderAt, size int64) error {
	_, err := privateWorkspaceReader(reader, size, workspaceStreamLimits)
	return err
}
func ValidatePrivateWorkspaceFileInterpreters(reader io.ReaderAt, size int64) error {
	files, err := privateWorkspaceReader(reader, size, workspaceStreamLimits)
	if err != nil {
		return err
	}
	return validatePrivateWorkspaceInterpreters(files)
}
func InstallPrivateWorkspaceFile(ctx context.Context, root string, reader io.ReaderAt, size int64) error {
	files, err := privateWorkspaceReader(workspaceContextReaderAt{ctx: ctx, reader: reader}, size, workspaceStreamLimits)
	if err != nil {
		return err
	}
	return installPrivateWorkspaceEntries(ctx, root, files, nil)
}
