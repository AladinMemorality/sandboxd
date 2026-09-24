package runtime

// Private home bundles are reviewed, same-owner migration artifacts. They must
// never be used for publication/remix. Callers fence all owner writes throughout
// validation, export/import and provider commit, and retain durable recovery ZIPs.
import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/sys/unix"
)

const MaxPrivateHomeBytes int64 = 4 << 30
const MaxPrivateHomeExpandedBytes int64 = 8 << 30
const MaxPrivateHomeFileBytes int64 = 1 << 30
const MaxPrivateHomeEntries = 500000
const MaxHomeManifestBytes = 4096
const MaxHomeManifestV2Bytes = 32 << 10

type HomeManifest struct {
	Version      int                 `json:"version"`
	Entries      []HomeManifestEntry `json:"entries"`
	Links        []HomeLinkContract  `json:"links,omitempty"`
	LiteralPaths []string            `json:"literal_paths,omitempty"`
}
type HomeManifestEntry struct {
	Path        string `json:"path"`
	Disposition string `json:"disposition"`
	Reason      string `json:"reason,omitempty"`
}
type HomeReport struct {
	Eligible         bool     `json:"eligible"`
	Reasons          []string `json:"reasons,omitempty"`
	PreservedEntries int      `json:"preserved_entries"`
	PreservedBytes   int64    `json:"preserved_bytes"`
	StockEntries     int      `json:"stock_entries"`
	RetainedEntries  int      `json:"retained_entries"`
	RetainedBytes    int64    `json:"retained_bytes"`
	SeparateEntries  int      `json:"separate_entries"`
	needsLinks       bool
}

var stockHomeHashes = map[string]string{
	".bashrc":    "d28e0f5fb00ce9f17f21ac66ce05b4ae4e541b9459028639e3f848b50c8f3ffe",
	".profile":   "d755f668dc89c4fa612a24ba44f56a497c6f102acebf7adba95eabb89654eade",
	".gitconfig": "6ee87852c22fa105f05076b886814f185fa685269d4f27a8e0c94b046e3f0328",
}

const emptyHomeHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

func stockHomeHash(name string) string {
	if hash := stockHomeHashes[name]; hash != "" {
		return hash
	}
	for _, dir := range []string{"workspace", ".cache", ".config", ".bun", ".npm-global"} {
		if name == dir+"/.gitkeep" {
			return emptyHomeHash
		}
	}
	return ""
}
func beneath(name, root string) bool { return name == root || strings.HasPrefix(name, root+"/") }
func sensitiveHomePath(name string) bool {
	if strings.HasPrefix(name, ".claude.json.") {
		return true
	}
	for _, root := range []string{".runtimed", ".ssh", ".aws", ".azure", ".gnupg", ".kube", ".docker", ".pki", ".claude", ".claude.json", ".codex", ".gemini", ".config/gcloud", ".config/opencode", ".local/share/opencode", ".git-credentials", ".netrc", ".npmrc"} {
		if beneath(name, root) {
			return true
		}
	}
	for _, leaf := range []string{"auth.json", ".credentials.json", "credentials.json", ".git-credentials", ".netrc", ".npmrc"} {
		if path.Base(name) == leaf {
			return true
		}
	}
	return false
}
func reviewedProviderDocument(name string) bool {
	return strings.HasPrefix(name, ".claude/plans/") && (strings.HasSuffix(name, ".md") || strings.HasSuffix(name, ".txt"))
}
func CanonicalHomeManifest(manifest HomeManifest) ([]byte, error) {
	if (manifest.Version != 1 && manifest.Version != 2) || len(manifest.Entries) > 96 {
		return nil, errors.New("unsupported home manifest")
	}
	manifest.Entries = append([]HomeManifestEntry(nil), manifest.Entries...)
	sort.Slice(manifest.Entries, func(i, j int) bool { return manifest.Entries[i].Path < manifest.Entries[j].Path })
	for i, entry := range manifest.Entries {
		if !ValidArchivePath(entry.Path) || entry.Path == "." || len(entry.Reason) > 256 {
			return nil, errors.New("invalid home manifest path or reason")
		}
		for j := 0; j < i; j++ {
			if beneath(entry.Path, manifest.Entries[j].Path) {
				return nil, errors.New("home manifest scopes overlap")
			}
		}
		switch entry.Disposition {
		case "preserve":
			if entry.Path == "workspace" || beneath(entry.Path, "workspace/app") || (sensitiveHomePath(entry.Path) && !reviewedProviderDocument(entry.Path)) {
				return nil, errors.New("home preserve scope includes protected runtime or provider identity")
			}
		case "stock":
			if stockHomeHash(entry.Path) == "" {
				return nil, errors.New("home stock path is not reviewed")
			}
		case "retained":
			if entry.Reason == "" || !sensitiveHomePath(entry.Path) || beneath(entry.Path, ".runtimed") {
				return nil, errors.New("retained scope requires a reviewed provider/auth path and reason")
			}
		case "separate":
			if entry.Path != "workspace/app" && entry.Path != ".runtimed" {
				return nil, errors.New("unknown separate home transport")
			}
		default:
			return nil, errors.New("invalid home disposition")
		}
	}
	hasRuntime := false
	for _, entry := range manifest.Entries {
		if entry.Path == ".runtimed" && entry.Disposition == "separate" {
			hasRuntime = true
		}
	}
	if !hasRuntime {
		return nil, errors.New("home manifest must identify separate runtime scope")
	}
	if e := canonicalHomeLinks(&manifest); e != nil {
		return nil, e
	}
	raw, e := json.Marshal(manifest)
	if e != nil {
		return nil, e
	}
	limit := MaxHomeManifestBytes
	if manifest.Version == 2 {
		limit = MaxHomeManifestV2Bytes
	}
	if len(raw) > limit {
		return nil, errors.New("home manifest limit")
	}
	return raw, nil
}
func homeDisposition(name string, m HomeManifest) (HomeManifestEntry, bool) {
	for _, entry := range m.Entries {
		if beneath(name, entry.Path) {
			return entry, true
		}
	}
	for _, entry := range m.Entries {
		if strings.HasPrefix(entry.Path, name+"/") {
			return HomeManifestEntry{Path: name, Disposition: "ancestor"}, true
		}
	}
	return HomeManifestEntry{}, false
}
func privateHomeLink(name, target string) bool {
	if strings.HasPrefix(target, "/home/sandbox/") {
		return ValidArchivePath(strings.TrimPrefix(target, "/home/sandbox/"))
	}
	return privateLink(name, target)
}
func openHomePath(root *os.File, name string, directory bool) (*os.File, error) {
	parts := strings.Split(name, "/")
	current := root
	var owned *os.File
	for i, part := range parts {
		next, e := privateChild(current, part, i < len(parts)-1 || directory)
		if owned != nil {
			owned.Close()
		}
		if e != nil {
			return nil, e
		}
		owned = next
		current = next
	}
	return owned, nil
}
func walkPrivateHome(ctx context.Context, root *os.File, manifest HomeManifest, visit func(*os.File, string, string, unix.Stat_t) error) error {
	count := 0
	var walk func(*os.File, string, int) error
	walk = func(dir *os.File, prefix string, depth int) error {
		if depth > 32 {
			return errors.New("home depth limit")
		}
		for {
			names, e := dir.Readdirnames(512)
			if e != nil && e != io.EOF {
				return e
			}
			if len(names) == 0 {
				return nil
			}
			sort.Strings(names)
			for _, name := range names {
				if e = ctx.Err(); e != nil {
					return e
				}
				count++
				if count > MaxPrivateHomeEntries {
					return errors.New("home entry limit")
				}
				full := path.Join(prefix, name)
				if !privateHomePath(manifest, full) {
					return errors.New("invalid home path")
				}
				var st unix.Stat_t
				if e = unix.Fstatat(int(dir.Fd()), name, &st, unix.AT_SYMLINK_NOFOLLOW); e != nil {
					return e
				}
				if e = visit(dir, name, full, st); e == filepath.SkipDir && st.Mode&unix.S_IFMT == unix.S_IFDIR {
					continue
				} else if e != nil {
					return e
				}
				if st.Mode&unix.S_IFMT == unix.S_IFDIR {
					child, e := privateChild(dir, name, true)
					if e != nil {
						return e
					}
					e = walk(child, full, depth+1)
					child.Close()
					if e != nil {
						return e
					}
				}
			}
		}
	}
	return walk(root, "", 0)
}
func ValidateHomeManifest(ctx context.Context, home string, manifest HomeManifest) (HomeReport, error) {
	report := HomeReport{}
	if _, e := CanonicalHomeManifest(manifest); e != nil {
		return report, e
	}
	root, e := privateDirectory(home)
	if e != nil {
		return report, e
	}
	defer root.Close()
	found := map[string]bool{}
	var expanded int64
	e = walkPrivateHome(ctx, root, manifest, func(parent *os.File, leaf, name string, st unix.Stat_t) error {
		entry, ok := homeDisposition(name, manifest)
		if !ok {
			return fmt.Errorf("unclassified home path: %s", name)
		}
		if name == entry.Path {
			found[name] = true
		}
		kind := st.Mode & unix.S_IFMT
		if strings.Contains(name, "\\") && kind != unix.S_IFREG {
			return errors.New("literal home package path is not regular")
		}
		if (entry.Disposition == "preserve" || entry.Disposition == "stock") && kind == unix.S_IFREG {
			if st.Size < 0 || st.Size > MaxPrivateHomeExpandedBytes-expanded {
				return errors.New("expanded home limit")
			}
			expanded += st.Size
		}
		switch entry.Disposition {
		case "ancestor":
			if kind != unix.S_IFDIR {
				return errors.New("home structural ancestor is not a directory")
			}
		case "separate":
			report.SeparateEntries++
			if kind == unix.S_IFDIR {
				return filepath.SkipDir
			}
		case "retained":
			report.RetainedEntries++
			if kind == unix.S_IFREG {
				report.RetainedBytes += st.Size
			}
		case "stock":
			if name != entry.Path || kind != unix.S_IFREG || st.Nlink != 1 || st.Size > 1<<20 {
				return errors.New("unsafe stock home file")
			}
			f, e := privateChild(parent, leaf, false)
			if e != nil {
				return e
			}
			hash := sha256.New()
			_, e = io.Copy(hash, io.LimitReader(f, (1<<20)+1))
			f.Close()
			if e != nil {
				return e
			}
			if hex.EncodeToString(hash.Sum(nil)) != stockHomeHash(name) {
				return fmt.Errorf("stock home content differs: %s", name)
			}
			report.StockEntries++
		case "preserve":
			if sensitiveHomePath(name) && !(name == entry.Path && reviewedProviderDocument(name) && kind == unix.S_IFREG) {
				return fmt.Errorf("preserve scope contains provider/auth state: %s", name)
			}
			if kind != unix.S_IFREG && kind != unix.S_IFDIR && kind != unix.S_IFLNK {
				return errors.New("preserved home contains special file")
			}
			if kind == unix.S_IFREG {
				if st.Nlink != 1 {
					report.needsLinks = true
				}
				if st.Size > MaxPrivateHomeFileBytes {
					return errors.New("home file limit")
				}
				report.PreservedBytes += st.Size
			}
			if kind == unix.S_IFLNK {
				buf := make([]byte, 4097)
				n, e := unix.Readlinkat(int(parent.Fd()), leaf, buf)
				if e != nil {
					return e
				}
				if n > 4096 || !manifestHomeLink(manifest, name, string(buf[:n])) {
					return errors.New("home symlink escapes owner scope")
				}
				if int64(n) > MaxPrivateHomeExpandedBytes-expanded {
					return errors.New("expanded home limit")
				}
				expanded += int64(n)
			}
			report.PreservedEntries++
		}
		return nil
	})
	if e == nil {
		for _, entry := range manifest.Entries {
			if !found[entry.Path] && entry.Disposition != "retained" {
				e = fmt.Errorf("manifest path missing: %s", entry.Path)
				break
			}
		}
	}
	if e == nil && report.PreservedBytes > MaxPrivateHomeExpandedBytes {
		e = errors.New("expanded home limit")
	}
	report.Eligible = e == nil
	if e != nil {
		report.Reasons = []string{e.Error()}
	}
	return report, e
}

type homeContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r homeContextReader) Read(p []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.reader.Read(p)
}

type homeContextReaderAt struct {
	ctx    context.Context
	reader io.ReaderAt
}

func (r homeContextReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.reader.ReadAt(p, off)
}

type homeBoundedWriter struct {
	w io.Writer
	n int64
}

func (w *homeBoundedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > MaxPrivateHomeBytes-w.n {
		return 0, errors.New("compressed home limit")
	}
	n, e := w.w.Write(p)
	w.n += int64(n)
	return n, e
}
func ExportPrivateHome(ctx context.Context, home string, manifest HomeManifest, dest io.Writer) error {
	report, e := ValidateHomeManifest(ctx, home, manifest)
	if e != nil {
		return e
	}
	// Owner writes are fenced: inode inventory proves every link to a copied
	// multiply-linked UID1000 file stays in this exact private owner's home.
	var links map[privateInode]uint64
	if report.needsLinks {
		links, e = inventoryOwnerLinks(ctx, home)
		if e != nil {
			return e
		}
	}
	root, e := privateDirectory(home)
	if e != nil {
		return e
	}
	defer root.Close()
	writer := zip.NewWriter(&homeBoundedWriter{w: dest})
	var expanded, indexBytes int64
	e = walkPrivateHome(ctx, root, manifest, func(parent *os.File, leaf, name string, st unix.Stat_t) error {
		entry, _ := homeDisposition(name, manifest)
		if entry.Disposition != "preserve" && entry.Disposition != "stock" {
			if entry.Disposition != "ancestor" && st.Mode&unix.S_IFMT == unix.S_IFDIR {
				return filepath.SkipDir
			}
			return nil
		}
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetMode(os.FileMode(st.Mode & 0777))
		kind := st.Mode & unix.S_IFMT
		indexBytes += int64(46 + len(name))
		if kind == unix.S_IFDIR {
			indexBytes++
		}
		if indexBytes > 64<<20 {
			return errors.New("home archive index limit")
		}
		if kind == unix.S_IFDIR {
			header.Name += "/"
			header.SetMode(os.ModeDir | os.FileMode(st.Mode&0777))
			_, e := writer.CreateHeader(header)
			return e
		}
		if kind == unix.S_IFLNK {
			buf := make([]byte, 4097)
			n, e := unix.Readlinkat(int(parent.Fd()), leaf, buf)
			if e != nil {
				return e
			}
			if n > 4096 || !manifestHomeLink(manifest, name, string(buf[:n])) {
				return errors.New("invalid home link")
			}
			if int64(n) > MaxPrivateHomeExpandedBytes-expanded {
				return errors.New("expanded home limit")
			}
			expanded += int64(n)
			header.SetMode(os.ModeSymlink | 0777)
			w, e := writer.CreateHeader(header)
			if e != nil {
				return e
			}
			_, e = w.Write(buf[:n])
			return e
		}
		f, e := privateChild(parent, leaf, false)
		if e != nil {
			return e
		}
		defer f.Close()
		var actual unix.Stat_t
		if e = unix.Fstat(int(f.Fd()), &actual); e != nil {
			return e
		}
		if actual.Mode&unix.S_IFMT != unix.S_IFREG || actual.Size < 0 || actual.Size > MaxPrivateHomeFileBytes || actual.Size > MaxPrivateHomeExpandedBytes-expanded || (actual.Nlink != 1 && (actual.Uid != 1000 || links[privateInode{uint64(actual.Dev), actual.Ino}] != uint64(actual.Nlink))) {
			return errors.New("invalid or externally linked home file")
		}
		expanded += actual.Size
		w, e := writer.CreateHeader(header)
		if e != nil {
			return e
		}
		n, e := io.Copy(w, io.LimitReader(homeContextReader{ctx: ctx, reader: f}, actual.Size+1))
		if e != nil {
			return e
		}
		if n != actual.Size {
			return errors.New("home file changed or exceeded limit")
		}
		return nil
	})
	if e != nil {
		return e
	}
	if e = writer.Close(); e != nil {
		return e
	}
	return nil
}
func homeArchive(manifest HomeManifest, reader io.ReaderAt, size int64) ([]*zip.File, error) {
	if _, e := CanonicalHomeManifest(manifest); e != nil {
		return nil, e
	}
	if size < 0 || size > MaxPrivateHomeBytes {
		return nil, errors.New("home archive limit")
	}
	if e := ValidatePrivateArchiveIndex(reader, size, MaxPrivateHomeEntries); e != nil {
		return nil, e
	}
	z, e := zip.NewReader(reader, size)
	if e != nil {
		return nil, e
	}
	if len(z.File) > MaxPrivateHomeEntries {
		return nil, errors.New("home archive entry limit")
	}
	seen := map[string]os.FileMode{}
	var expanded uint64
	for _, f := range z.File {
		name := strings.TrimSuffix(f.Name, "/")
		entry, ok := homeDisposition(name, manifest)
		if !privateHomePath(manifest, name) || !ok || (entry.Disposition != "preserve" && entry.Disposition != "stock") {
			return nil, errors.New("archive outside preserved home scope")
		}
		mode := f.Mode()
		if strings.Contains(name, "\\") && !mode.IsRegular() {
			return nil, errors.New("literal home package archive path is not regular")
		}
		if (!mode.IsRegular() && !mode.IsDir() && mode&os.ModeSymlink == 0) || sensitiveHomePath(name) && !(name == entry.Path && reviewedProviderDocument(name) && mode.IsRegular()) {
			return nil, errors.New("unsafe home archive entry")
		}
		if _, ok := seen[name]; ok {
			return nil, errors.New("duplicate home archive entry")
		}
		seen[name] = mode
		if f.UncompressedSize64 > uint64(MaxPrivateHomeFileBytes) || f.UncompressedSize64 > uint64(MaxPrivateHomeExpandedBytes)-expanded {
			return nil, errors.New("expanded home archive limit")
		}
		expanded += f.UncompressedSize64
		if mode.IsDir() && f.UncompressedSize64 != 0 {
			return nil, errors.New("nonempty directory entry")
		}
	}
	for _, entry := range manifest.Entries {
		if entry.Disposition == "preserve" || entry.Disposition == "stock" {
			if _, ok := seen[entry.Path]; !ok {
				return nil, errors.New("preserved root missing from archive")
			}
		}
	}
	for _, f := range z.File {
		name := strings.TrimSuffix(f.Name, "/")
		for p := path.Dir(name); p != "."; p = path.Dir(p) {
			if mode, ok := seen[p]; ok && !mode.IsDir() {
				return nil, errors.New("home archive ancestor is not directory")
			}
		}
		entry, _ := homeDisposition(name, manifest)
		if entry.Disposition == "stock" {
			if !f.Mode().IsRegular() || name != entry.Path || f.UncompressedSize64 > 1<<20 {
				return nil, errors.New("invalid stock home archive")
			}
			r, e := f.Open()
			if e != nil {
				return nil, e
			}
			h := sha256.New()
			_, e = io.Copy(h, r)
			r.Close()
			if e != nil || hex.EncodeToString(h.Sum(nil)) != stockHomeHash(name) {
				return nil, errors.New("stock home archive hash differs")
			}
		}
		if f.Mode().IsDir() {
			continue
		}
		r, e := f.Open()
		if e != nil {
			return nil, e
		}
		if f.Mode()&os.ModeSymlink != 0 {
			if f.UncompressedSize64 > 4096 {
				r.Close()
				return nil, errors.New("home link limit")
			}
			b, e := io.ReadAll(r)
			r.Close()
			if e != nil || !manifestHomeLink(manifest, name, string(b)) {
				return nil, errors.New("home link escapes scope")
			}
		} else {
			n, e := io.Copy(io.Discard, io.LimitReader(r, MaxPrivateHomeFileBytes+1))
			r.Close()
			if e != nil || uint64(n) != f.UncompressedSize64 {
				return nil, errors.New("invalid home archive data")
			}
		}
	}
	return z.File, nil
}
func PrivateHomeDigest(manifest HomeManifest, reader io.ReaderAt, size int64) (string, error) {
	files, e := homeArchive(manifest, reader, size)
	if e != nil {
		return "", e
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	h := sha256.New()
	raw, _ := CanonicalHomeManifest(manifest)
	binary.Write(h, binary.BigEndian, uint64(len(raw)))
	h.Write(raw)
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

// Install replaces each disjoint selected root while the guest stays quiesced.
// This is not an atomic transaction across roots: interruption requires retry
// from the journaled ZIP before resume. Each individual replacement is atomic.
func InstallPrivateHome(ctx context.Context, home string, manifest HomeManifest, reader io.ReaderAt, size int64) error {
	files, e := homeArchive(manifest, homeContextReaderAt{ctx: ctx, reader: reader}, size)
	if e != nil {
		return e
	}
	root, e := privateDirectory(home)
	if e != nil {
		return e
	}
	defer root.Close()
	// Create staging through the pinned descriptor, never a replaceable ancestor.
	if e = unix.Mkdirat(int(root.Fd()), ".runtimed", 0700); e != nil && e != unix.EEXIST {
		return e
	}
	runtimeDir, e := privateChild(root, ".runtimed", true)
	if e != nil {
		return e
	}
	defer runtimeDir.Close()
	stage, e := os.MkdirTemp(fmt.Sprintf("/proc/self/fd/%d", runtimeDir.Fd()), ".home-import-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(stage)
	for _, f := range files {
		if e = ctx.Err(); e != nil {
			return e
		}
		dest := filepath.Join(stage, filepath.FromSlash(f.Name))
		if f.Mode()&os.ModeSymlink != 0 {
			continue
		}
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
		w, e := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, f.Mode().Perm())
		if e != nil {
			r.Close()
			return e
		}
		_, e = io.Copy(w, homeContextReader{ctx: ctx, reader: r})
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
		if f.Mode()&os.ModeSymlink == 0 {
			continue
		}
		r, e := f.Open()
		if e != nil {
			return e
		}
		b, e := io.ReadAll(r)
		r.Close()
		if e != nil {
			return e
		}
		dest := filepath.Join(stage, filepath.FromSlash(f.Name))
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
			if e = os.Chmod(filepath.Join(stage, filepath.FromSlash(f.Name)), f.Mode().Perm()); e != nil {
				return e
			}
		}
	}
	// syncPrivateTree intentionally refuses /proc symlinks: use the actual path
	// only while root ancestry is revalidated and pinned for final rename calls.
	stageReal := filepath.Join(home, ".runtimed", filepath.Base(stage))
	if e = syncPrivateTree(stageReal); e != nil {
		return e
	}
	staged, e := privateChild(runtimeDir, filepath.Base(stage), true)
	if e != nil {
		return e
	}
	defer staged.Close()
	for _, entry := range manifest.Entries {
		if entry.Disposition != "preserve" && entry.Disposition != "stock" {
			continue
		}
		if e = ctx.Err(); e != nil {
			return e
		}
		// Ancestors are structure only; creating them does not import any identity.
		parentName := path.Dir(entry.Path)
		parent := root
		var owned *os.File
		if parentName != "." {
			current := root
			for _, part := range strings.Split(parentName, "/") {
				if e = unix.Mkdirat(int(current.Fd()), part, 0755); e != nil && e != unix.EEXIST {
					if owned != nil {
						owned.Close()
					}
					return e
				}
				if e = current.Sync(); e != nil {
					if owned != nil {
						owned.Close()
					}
					return e
				}
				next, e := privateChild(current, part, true)
				if owned != nil {
					owned.Close()
				}
				if e != nil {
					return e
				}
				owned = next
				current = next
			}
			parent = owned
		}
		leaf := path.Base(entry.Path)
		var st unix.Stat_t
		e = unix.Fstatat(int(parent.Fd()), leaf, &st, unix.AT_SYMLINK_NOFOLLOW)
		flags := uint(unix.RENAME_EXCHANGE)
		if e == unix.ENOENT {
			flags = unix.RENAME_NOREPLACE
		} else if e != nil {
			if owned != nil {
				owned.Close()
			}
			return e
		}
		e = unix.Renameat2(int(staged.Fd()), entry.Path, int(parent.Fd()), leaf, flags)
		if e == nil {
			e = parent.Sync()
		}
		if owned != nil {
			owned.Close()
		}
		if e != nil {
			return e
		}
	}
	return root.Sync()
}
