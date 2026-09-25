package api

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

var errUnsafeFilePath = errors.New("unsafe file path")

// Keep every ancestor open until the operation completes. Rechecking the
// parent/name identity detects replacements during a slow upload; subsequent
// operations still use FDs, never the checked pathname (even after a race).
type fileDirChain struct {
	fds   []int
	names []string
}

func (c *fileDirChain) close() {
	for _, fd := range c.fds {
		_ = unix.Close(fd)
	}
}
func (c *fileDirChain) last() int { return c.fds[len(c.fds)-1] }
func (c *fileDirChain) unchanged() error {
	for i, name := range c.names {
		var named, opened unix.Stat_t
		if unix.Fstatat(c.fds[i], name, &named, unix.AT_SYMLINK_NOFOLLOW) != nil || unix.Fstat(c.fds[i+1], &opened) != nil || named.Mode&unix.S_IFMT != unix.S_IFDIR || named.Dev != opened.Dev || named.Ino != opened.Ino {
			return errUnsafeFilePath
		}
	}
	return nil
}

func (c *fileDirChain) descend(name string, create bool, uid, gid int) error {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\x00") {
		return errUnsafeFilePath
	}
	created := false
	fd, err := unix.Openat(c.last(), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOENT) && create {
		err = unix.Mkdirat(c.last(), name, 0775)
		if err != nil && !errors.Is(err, unix.EEXIST) {
			return err
		}
		created = err == nil
		fd, err = unix.Openat(c.last(), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	}
	if err != nil {
		if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENOTDIR) {
			return errUnsafeFilePath
		}
		return err
	}
	c.fds = append(c.fds, fd)
	c.names = append(c.names, name)
	if created {
		return unix.Fchown(fd, uid, gid)
	}
	return nil
}

func openWorkspaceDirs(mnt string) (*fileDirChain, unix.Stat_t, error) {
	var owner unix.Stat_t
	if !filepath.IsAbs(mnt) {
		return nil, owner, errUnsafeFilePath
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, owner, err
	}
	c := &fileDirChain{fds: []int{fd}}
	for _, part := range strings.Split(strings.TrimPrefix(filepath.Clean(mnt), "/"), "/") {
		if err = c.descend(part, false, -1, -1); err != nil {
			c.close()
			return nil, owner, err
		}
	}
	if err = unix.Fstat(c.last(), &owner); err != nil {
		c.close()
		return nil, owner, err
	}
	return c, owner, nil
}

func regularLeaf(dir int, name string) error {
	var st unix.Stat_t
	err := unix.Fstatat(dir, name, &st, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG {
		return errUnsafeFilePath
	}
	return nil
}

func openAppDirs(mnt, rel string) (*fileDirChain, error) {
	c, _, err := openWorkspaceDirs(mnt)
	if err != nil {
		return nil, err
	}
	path := appSubdir
	if rel != "" && rel != "." {
		path += "/" + rel
	}
	for _, part := range strings.Split(path, "/") {
		if err = c.descend(part, false, -1, -1); err != nil {
			c.close()
			return nil, err
		}
	}
	if err = c.unchanged(); err != nil {
		c.close()
		return nil, err
	}
	return c, nil
}

func openRegularAt(dir int, name string) (*os.File, error) {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\x00") {
		return nil, errUnsafeFilePath
	}
	fd, err := unix.Openat(dir, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), name)
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		f.Close()
		return nil, errUnsafeFilePath
	}
	return f, nil
}

// The callback consumes each file while its parent is retained. Descendants
// are opened relative to that FD; a directory replaced by a symlink is skipped.
func walkAppFiles(dir int, prefix string, recursive bool, visit func(string, bool, *os.File) error) error {
	fd, err := unix.Openat(dir, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), ".")
	defer f.Close()
	for {
		entries, readErr := f.ReadDir(128)
		for _, entry := range entries {
			if excludedFromFiles[entry.Name()] || entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			name := entry.Name()
			rel := name
			if prefix != "" {
				rel = prefix + "/" + name
			}
			if entry.IsDir() {
				child, e := unix.Openat(fd, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
				if e != nil {
					continue
				}
				e = visit(rel, true, nil)
				if e == nil && recursive {
					e = walkAppFiles(child, rel, true, visit)
				}
				unix.Close(child)
				if e != nil {
					return e
				}
			} else {
				file, e := openRegularAt(fd, name)
				if e != nil {
					continue
				}
				e = visit(rel, false, file)
				file.Close()
				if e != nil {
					return e
				}
			}
		}
		if errors.Is(readErr, io.EOF) {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}

func writeAppFile(mnt, rel string, body io.Reader) (int64, error) {
	c, owner, err := openWorkspaceDirs(mnt)
	if err != nil {
		return 0, err
	}
	defer c.close()
	parts := strings.Split(appSubdir+"/"+rel, "/")
	for _, part := range parts[:len(parts)-1] {
		if err = c.descend(part, true, int(owner.Uid), int(owner.Gid)); err != nil {
			return 0, err
		}
	}
	dir, leaf := c.last(), parts[len(parts)-1]
	if err = regularLeaf(dir, leaf); err != nil {
		return 0, err
	}
	var nonce [24]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return 0, err
	}
	name := ".put-" + hex.EncodeToString(nonce[:]) + ".tmp"
	fd, err := unix.Openat(dir, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return 0, err
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	defer unix.Unlinkat(dir, name, 0)
	written, err := io.Copy(f, body)
	if err != nil {
		return written, err
	}
	if err = c.unchanged(); err != nil {
		return written, err
	}
	if err = regularLeaf(dir, leaf); err != nil {
		return written, err
	}
	if err = f.Chown(int(owner.Uid), int(owner.Gid)); err != nil {
		return written, err
	}
	if err = f.Chmod(0644); err != nil {
		return written, err
	}
	// Ensure the temporary directory entry still names our open file. All
	// ownership/mode/content operations used its FD, so a substituted link can
	// never redirect a privileged write. Rename replaces a leaf, not its target.
	var named, opened unix.Stat_t
	if unix.Fstatat(dir, name, &named, unix.AT_SYMLINK_NOFOLLOW) != nil || unix.Fstat(fd, &opened) != nil || named.Dev != opened.Dev || named.Ino != opened.Ino || named.Mode&unix.S_IFMT != unix.S_IFREG {
		return written, errUnsafeFilePath
	}
	if err = f.Sync(); err != nil {
		return written, err
	}
	if err = unix.Renameat(dir, name, dir, leaf); err != nil {
		return written, err
	}
	return written, unix.Fsync(dir)
}
