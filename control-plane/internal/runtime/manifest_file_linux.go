package runtime

import (
	"errors"
	"golang.org/x/sys/unix"
	"io"
	"os"
)

// ReadAppManifest reads only the regular sandbox.yaml beneath a trusted app
// root. Descriptor-relative opens reject symlink/special-file host escapes.
func ReadAppManifest(root string) ([]byte, bool, error) {
	dir, err := privateDirectory(root)
	if err != nil {
		return nil, false, err
	}
	defer dir.Close()
	file, err := privateChild(dir, "sandbox.yaml", false)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer file.Close()
	var stat unix.Stat_t
	if err = unix.Fstat(int(file.Fd()), &stat); err != nil {
		return nil, false, err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Nlink != 1 || stat.Size > 1<<20 {
		return nil, false, errors.New("manifest must be a bounded regular file")
	}
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return nil, false, errors.New("manifest read failed or exceeds limit")
	}
	return data, true, nil
}
