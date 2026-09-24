package runtime

// Private workspace archives are ONLY for an existing owner's migration and
// rollback. They intentionally contain secrets/data/.git and MUST NOT be used
// for publication, remix, or any transfer to a different owner.
import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"golang.org/x/sys/unix"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

const MaxPrivateWorkspaceBytes = 256 << 20
const MaxPrivateWorkspaceEntries = 200000
const MaxPrivateWorkspaceExpandedBytes = 4 << 30
const MaxPrivateWorkspaceFileBytes = 128 << 20

func privateLink(name, target string) bool {
	if trustedPrivateInterpreterLink(name, target) {
		return true
	}
	if target == "" || path.IsAbs(target) || strings.ContainsAny(target, "\\\x00") {
		return false
	}
	resolved := path.Clean(path.Join(path.Dir(name), target))
	return resolved != ".." && !strings.HasPrefix(resolved, "../")
}
func privateDirectory(root string) (*os.File, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("workspace root must be absolute")
	}
	fd, e := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, e
	}
	for _, part := range strings.Split(strings.TrimPrefix(filepath.Clean(root), "/"), "/") {
		if part == "" {
			continue
		}
		next, e := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if e != nil {
			return nil, e
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), root), nil
}
func privateChild(parent *os.File, name string, directory bool) (*os.File, error) {
	flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
	if directory {
		flags |= unix.O_DIRECTORY
	}
	fd, e := unix.Openat(int(parent.Fd()), name, flags, 0)
	if e != nil {
		return nil, e
	}
	return os.NewFile(uintptr(fd), name), nil
}
func ExportPrivateWorkspace(root string) ([]byte, error) {
	return ExportPrivateWorkspaceContext(context.Background(), root)
}
func ExportPrivateWorkspaceContext(ctx context.Context, root string) ([]byte, error) {
	return exportPrivateWorkspace(ctx, root, nil)
}

var errPrivateWorkspaceHardlink = errors.New("workspace hardlink requires owner proof")

type privateInode struct {
	dev uint64
	ino uint64
}

func ExportPrivateWorkspaceOwnerContext(ctx context.Context, root, ownerHome string) ([]byte, error) {
	rel, e := filepath.Rel(ownerHome, root)
	if e != nil || rel == ".." || strings.HasPrefix(rel, "../") || filepath.IsAbs(rel) {
		return nil, errors.New("workspace outside owner home")
	}
	// Imported archives flatten hardlinks. Avoid inspecting unrelated home state
	// unless an opened workspace file actually needs the owner-link proof.
	data, e := exportPrivateWorkspace(ctx, root, nil)
	if !errors.Is(e, errPrivateWorkspaceHardlink) {
		return data, e
	}
	links, e := inventoryOwnerLinks(ctx, ownerHome)
	if e != nil {
		return nil, e
	}
	return exportPrivateWorkspace(ctx, root, links)
}
func exportPrivateWorkspace(ctx context.Context, root string, ownedLinks map[privateInode]uint64) ([]byte, error) {
	var out privateArchiveBuffer
	if err := exportPrivateWorkspaceTo(ctx, root, &out, workspaceV1Limits, func() (map[privateInode]uint64, error) { return ownedLinks, nil }); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func ValidatePrivateWorkspaceArchive(data []byte) error {
	_, e := privateWorkspaceEntries(data)
	return e
}
func privateWorkspaceEntries(data []byte) ([]*zip.File, error) {
	return privateWorkspaceReader(bytes.NewReader(data), int64(len(data)), workspaceV1Limits)
}
func privateWorkspaceReader(reader io.ReaderAt, archiveBytes int64, limits workspaceArchiveLimits) ([]*zip.File, error) {
	if archiveBytes < 0 || archiveBytes > limits.compressed {
		return nil, errors.New("workspace archive limit")
	}
	if err := ValidatePrivateArchiveIndex(reader, archiveBytes, limits.entries); err != nil {
		return nil, err
	}
	z, e := zip.NewReader(reader, archiveBytes)
	if e != nil {
		return nil, e
	}
	if len(z.File) > limits.entries {
		return nil, errors.New("workspace entry limit")
	}
	seen := map[string]os.FileMode{}
	var size uint64
	for _, f := range z.File {
		name := strings.TrimSuffix(f.Name, "/")
		if !ValidArchivePath(name) {
			return nil, errors.New("invalid workspace path")
		}
		if _, ok := seen[name]; ok {
			return nil, errors.New("duplicate workspace path")
		}
		mode := f.Mode()
		if !mode.IsDir() && !mode.IsRegular() && mode&os.ModeSymlink == 0 {
			return nil, errors.New("special workspace entry")
		}
		seen[name] = mode
		if f.UncompressedSize64 > uint64(limits.expanded)-size || f.UncompressedSize64 > uint64(limits.file) {
			return nil, errors.New("expanded workspace limit")
		}
		size += f.UncompressedSize64
		if mode.IsDir() && f.UncompressedSize64 != 0 {
			return nil, errors.New("nonempty workspace directory entry")
		}
	}
	for _, f := range z.File {
		name := strings.TrimSuffix(f.Name, "/")
		for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
			if m, ok := seen[parent]; ok && !m.IsDir() {
				return nil, errors.New("workspace ancestor is not directory")
			}
		}
		if f.Mode().IsDir() {
			continue
		}
		r, e := f.Open()
		if e != nil {
			return nil, e
		}
		n, e := io.Copy(io.Discard, io.LimitReader(r, limits.file+1))
		r.Close()
		if e != nil || uint64(n) != f.UncompressedSize64 {
			return nil, errors.New("invalid workspace content")
		}
		if f.Mode()&os.ModeSymlink != 0 {
			if f.UncompressedSize64 > 4096 {
				return nil, errors.New("workspace link limit")
			}
			r, _ = f.Open()
			b, e := io.ReadAll(r)
			r.Close()
			if e != nil || !privateLink(name, string(b)) {
				return nil, errors.New("workspace symlink escapes root")
			}
		}
	}
	return z.File, nil
}

// Install atomically exchanges a fully validated staged tree. The caller must
// stop/fence ALL workspace writers before calling and keep them fenced through
// provider commit. Existing root must be a real directory, never a symlink.
func InstallPrivateWorkspace(root string, data []byte) error {
	return InstallPrivateWorkspacePrepared(root, data, nil)
}
func InstallPrivateWorkspacePrepared(root string, data []byte, prepare func(string) error) error {
	files, e := privateWorkspaceEntries(data)
	if e != nil {
		return e
	}
	return installPrivateWorkspaceEntries(context.Background(), root, files, prepare)
}
func installPrivateWorkspaceEntries(ctx context.Context, root string, files []*zip.File, prepare func(string) error) error {
	parent, e := privateDirectory(filepath.Dir(root))
	if e != nil {
		return e
	}
	defer parent.Close()
	old, e := privateChild(parent, filepath.Base(root), true)
	if e != nil {
		return e
	}
	old.Close()
	staged, e := os.MkdirTemp(filepath.Dir(root), ".private-import-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(staged)
	// Directories/files first, symlinks last. Validation prohibits any archive
	// entry beneath a symlink; extraction never follows an archive-provided link.
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if f.Mode()&os.ModeSymlink != 0 {
			continue
		}
		dest := filepath.Join(staged, filepath.FromSlash(f.Name))
		if f.Mode().IsDir() {
			if e = os.MkdirAll(dest, 0700); e != nil {
				return e
			}
			continue
		}
		if e = os.MkdirAll(filepath.Dir(dest), 0700); e != nil {
			return e
		}
		r, e := f.Open()
		if e != nil {
			return e
		}
		w, e := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, f.Mode().Perm())
		if e != nil {
			r.Close()
			return e
		}
		_, e = io.Copy(w, workspaceContextReader{ctx: ctx, reader: r})
		r.Close()
		if e == nil {
			e = w.Chmod(f.Mode().Perm())
		}
		if e == nil {
			e = w.Sync()
		}
		ce := w.Close()
		if e != nil {
			return e
		}
		if ce != nil {
			return ce
		}
	}
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if f.Mode()&os.ModeSymlink == 0 {
			continue
		}
		r, _ := f.Open()
		b, e := io.ReadAll(r)
		r.Close()
		if e != nil {
			return e
		}
		dest := filepath.Join(staged, filepath.FromSlash(f.Name))
		if e = os.MkdirAll(filepath.Dir(dest), 0700); e != nil {
			return e
		}
		if e = os.Symlink(string(b), dest); e != nil {
			return e
		}
	}
	for i := len(files) - 1; i >= 0; i-- {
		f := files[i]
		if f.Mode().IsDir() {
			if e = os.Chmod(filepath.Join(staged, filepath.FromSlash(f.Name)), f.Mode().Perm()); e != nil {
				return e
			}
		}
	}
	if e = os.Chmod(staged, 0755); e != nil {
		return e
	}
	if prepare != nil {
		if e = prepare(staged); e != nil {
			return e
		}
	}
	if e = syncPrivateTree(staged); e != nil {
		return e
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if e = unix.Renameat2(int(parent.Fd()), filepath.Base(staged), int(parent.Fd()), filepath.Base(root), unix.RENAME_EXCHANGE); e != nil {
		return e
	}
	return parent.Sync()
}
func (c *Client) QuiesceWorkspace(ctx context.Context) error {
	_, e := c.bounded(ctx, http.MethodPost, "/workspace/quiesce", nil, 4096)
	return e
}
func (c *Client) ResumeWorkspace(ctx context.Context) error {
	_, e := c.bounded(ctx, http.MethodPost, "/workspace/resume", nil, 4096)
	return e
}
func (c *Client) ExportPrivateWorkspace(ctx context.Context) ([]byte, error) {
	return c.bounded(ctx, http.MethodGet, "/export/private-workspace", nil, MaxPrivateWorkspaceBytes)
}
func (c *Client) ImportPrivateWorkspace(ctx context.Context, data []byte) error {
	if e := ValidatePrivateWorkspaceArchive(data); e != nil {
		return e
	}
	_, e := c.bounded(ctx, http.MethodPut, "/import/private-workspace", data, 4096)
	return e
}

// PrivateWorkspaceDigest compares logical trees, not ZIP order/compression/mtime.
func PrivateWorkspaceDigest(data []byte) (string, error) {
	files, e := privateWorkspaceEntries(data)
	if e != nil {
		return "", e
	}
	return privateWorkspaceDigestEntries(files)
}
func privateWorkspaceDigestEntries(files []*zip.File) (string, error) {
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	h := sha256.New()
	for _, f := range files {
		name := strings.TrimSuffix(f.Name, "/")
		binary.Write(h, binary.BigEndian, uint64(len(name)))
		io.WriteString(h, name)
		binary.Write(h, binary.BigEndian, uint32(f.Mode()))
		binary.Write(h, binary.BigEndian, f.UncompressedSize64)
		if !f.Mode().IsDir() {
			r, e := f.Open()
			if e != nil {
				return "", e
			}
			_, e = io.Copy(h, r)
			r.Close()
			if e != nil {
				return "", e
			}
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (c *Client) ImportGitWorkspace(ctx context.Context, data []byte) error {
	if e := ValidatePrivateWorkspaceArchive(data); e != nil {
		return e
	}
	_, e := c.bounded(ctx, http.MethodPut, "/import/git-workspace", data, 4096)
	return e
}

func syncPrivateTree(root string) error {
	dir, e := privateDirectory(root)
	if e != nil {
		return e
	}
	defer dir.Close()
	var syncDir func(*os.File, int) error
	syncDir = func(dir *os.File, depth int) error {
		if depth > 32 {
			return errors.New("workspace directory depth limit")
		}
		for {
			names, readErr := dir.Readdirnames(128)
			if readErr != nil && readErr != io.EOF {
				return readErr
			}
			for _, name := range names {
				var st unix.Stat_t
				if e = unix.Fstatat(int(dir.Fd()), name, &st, unix.AT_SYMLINK_NOFOLLOW); e != nil {
					return e
				}
				if st.Mode&unix.S_IFMT == unix.S_IFDIR {
					child, e := privateChild(dir, name, true)
					if e != nil {
						return e
					}
					e = syncDir(child, depth+1)
					child.Close()
					if e != nil {
						return e
					}
				}
			}
			if readErr == io.EOF {
				break
			}
		}
		return dir.Sync()
	}
	return syncDir(dir, 0)
}

// Inventory proves every link to a multiply-linked inode is beneath this one
// owner's home. Callers must first stop/fence that owner's writes. Symlinks are
// never followed, and traversal cannot enter another mounted filesystem.
func inventoryOwnerLinks(ctx context.Context, root string) (map[privateInode]uint64, error) {
	dir, e := privateDirectory(root)
	if e != nil {
		return nil, e
	}
	defer dir.Close()
	var rootStat unix.Statx_t
	if e = unix.Statx(int(dir.Fd()), "", unix.AT_EMPTY_PATH, unix.STATX_MNT_ID, &rootStat); e != nil || rootStat.Mask&unix.STATX_MNT_ID == 0 {
		return nil, errors.New("owner mount identity unavailable")
	}
	links := map[privateInode]uint64{}
	entries := 0
	var walk func(*os.File, int) error
	walk = func(dir *os.File, depth int) error {
		if depth > 64 {
			return errors.New("owner home depth limit")
		}
		for {
			names, readErr := dir.Readdirnames(128)
			if readErr != nil && readErr != io.EOF {
				return readErr
			}
			for _, name := range names {
				if e = ctx.Err(); e != nil {
					return e
				}
				entries++
				if entries > 1000000 {
					return errors.New("owner home inventory limit")
				}
				var st unix.Stat_t
				if e = unix.Fstatat(int(dir.Fd()), name, &st, unix.AT_SYMLINK_NOFOLLOW); e != nil {
					return e
				}
				var mount unix.Statx_t
				if e = unix.Statx(int(dir.Fd()), name, unix.AT_SYMLINK_NOFOLLOW, unix.STATX_MNT_ID, &mount); e != nil || mount.Mask&unix.STATX_MNT_ID == 0 || mount.Mnt_id != rootStat.Mnt_id {
					return errors.New("owner home crosses mount boundary")
				}
				switch st.Mode & unix.S_IFMT {
				case unix.S_IFREG:
					if st.Nlink > 1 && st.Uid == 1000 {
						links[privateInode{uint64(st.Dev), st.Ino}]++
					}
				case unix.S_IFDIR:
					child, e := privateChild(dir, name, true)
					if e != nil {
						return e
					}
					e = walk(child, depth+1)
					child.Close()
					if e != nil {
						return e
					}
				}
			}
			if readErr == io.EOF {
				break
			}
		}
		return nil
	}
	if e = walk(dir, 0); e != nil {
		return nil, e
	}
	return links, nil
}

type privateArchiveBuffer struct{ bytes.Buffer }

func (b *privateArchiveBuffer) Write(p []byte) (int, error) {
	if len(p) > MaxPrivateWorkspaceBytes-b.Len() {
		return 0, errors.New("compressed workspace limit")
	}
	return b.Buffer.Write(p)
}

// A virtualenv's interpreter is an executable leaf, not an exported directory.
// Only these exact guest tool paths are portable; all other absolute links fail.
func trustedPrivateInterpreterLink(name, target string) bool {
	parts := strings.Split(name, "/")
	if len(parts) < 3 || parts[len(parts)-3] != ".venv" || parts[len(parts)-2] != "bin" {
		return false
	}
	leaf := parts[len(parts)-1]
	if leaf != "python" && !pythonVersionLeaf(leaf) {
		return false
	}
	return strings.HasPrefix(target, "/usr/bin/") && pythonVersionLeaf(strings.TrimPrefix(target, "/usr/bin/"))
}
func pythonVersionLeaf(name string) bool {
	if name == "python3" {
		return true
	}
	if !strings.HasPrefix(name, "python3.") {
		return false
	}
	v := strings.TrimPrefix(name, "python3.")
	if len(v) < 1 || len(v) > 2 {
		return false
	}
	for _, c := range v {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// Call inside the destination guest before importing a private Python venv.
func ValidatePrivateWorkspaceInterpreters(data []byte) error {
	files, e := privateWorkspaceEntries(data)
	if e != nil {
		return e
	}
	return validatePrivateWorkspaceInterpreters(files)
}
func validatePrivateWorkspaceInterpreters(files []*zip.File) error {
	for _, f := range files {
		if f.Mode()&os.ModeSymlink == 0 {
			continue
		}
		r, _ := f.Open()
		b, e := io.ReadAll(r)
		r.Close()
		if e != nil {
			return e
		}
		if trustedPrivateInterpreterLink(f.Name, string(b)) {
			info, e := os.Stat(string(b))
			if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
				return errors.New("destination interpreter is unavailable")
			}
		}
	}
	return nil
}
