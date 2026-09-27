// Package projectsource defines private, portable source revisions. It is not a
// public/remix sanitizer and never claims to back up databases or guest memory.
package projectsource

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"
)

const MaxBytes = 256 << 20
const MaxFileBytes = 32 << 20
const MaxEntries = 10000

type Recipe struct {
	Version        int      `json:"version"`
	RuntimeImage   string   `json:"runtime_image"`
	PackageManager string   `json:"package_manager"`
	Lockfile       string   `json:"lockfile,omitempty"`
	Install        []string `json:"install,omitempty"`
	Build          []string `json:"build,omitempty"`
	Start          []string `json:"start"`
	Port           int      `json:"port"`
	HealthPath     string   `json:"health_path"`
	DataPaths      []string `json:"data_paths,omitempty"`
	SecretPaths    []string `json:"secret_paths,omitempty"`
	OutputPaths    []string `json:"output_paths,omitempty"`
}
type File struct {
	Path       string `json:"path"`
	SHA256     string `json:"sha256"`
	Bytes      int    `json:"bytes"`
	Executable bool   `json:"executable"`
}
type Exclusion struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}
type Manifest struct {
	Version       int         `json:"version"`
	Kind          string      `json:"kind"`
	Recipe        Recipe      `json:"recipe"`
	Files         []File      `json:"files"`
	Excluded      []Exclusion `json:"excluded"`
	SourceSHA256  string      `json:"source_sha256"`
	DependencyKey string      `json:"dependency_key"`
	Bytes         int         `json:"bytes"`
}

var runtimeImage = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]*@sha256:[a-f0-9]{64}$`)

func digest(b []byte) string { v := sha256.Sum256(b); return hex.EncodeToString(v[:]) }
func validPath(p string) bool {
	if p == "" || len(p) > 4096 || strings.ContainsAny(p, "\\\x00\r\n") || strings.HasPrefix(p, "/") || path.Clean(p) != p || p == "." || strings.HasPrefix(p, "../") || p == ".." || len(strings.Split(p, "/")) > 32 {
		return false
	}
	return true
}
func commandOK(c []string, required bool) bool {
	if len(c) == 0 {
		return !required
	}
	if len(c) > 128 || c[0] == "" {
		return false
	}
	n := 0
	for _, arg := range c {
		n += len(arg)
		if strings.ContainsRune(arg, 0) {
			return false
		}
	}
	return n <= 16384
}
func (r Recipe) Validate() error {
	if r.Version != 1 || !runtimeImage.MatchString(r.RuntimeImage) || !commandOK(r.Start, true) || !commandOK(r.Build, false) || r.Port < 1024 || r.Port > 65535 || !strings.HasPrefix(r.HealthPath, "/") || strings.HasPrefix(r.HealthPath, "//") || len(r.HealthPath) > 2048 || strings.ContainsAny(r.HealthPath, "\r\n\x00#") {
		return errors.New("invalid deployment recipe")
	}
	var expected []string
	switch r.PackageManager {
	case "none":
		if r.Lockfile != "" || len(r.Install) != 0 {
			return errors.New("dependency-free recipe cannot install packages")
		}
	case "npm":
		if r.Lockfile != "package-lock.json" && r.Lockfile != "npm-shrinkwrap.json" {
			return errors.New("npm requires a root lockfile")
		}
		expected = []string{"npm", "ci", "--no-audit", "--no-fund"}
	case "pnpm":
		if r.Lockfile != "pnpm-lock.yaml" {
			return errors.New("pnpm requires its lockfile")
		}
		expected = []string{"pnpm", "install", "--frozen-lockfile", "--config.package-import-method=copy"}
	case "bun":
		if r.Lockfile != "bun.lock" {
			return errors.New("bun requires its text lockfile")
		}
		expected = []string{"bun", "install", "--frozen-lockfile"}
	default:
		return errors.New("unsupported locked package manager")
	}
	if strings.Join(r.Install, "\x00") != strings.Join(expected, "\x00") {
		return errors.New("install command must use the reviewed frozen-lock recipe")
	}
	all := append(append(append([]string{}, r.DataPaths...), r.SecretPaths...), r.OutputPaths...)
	if len(all) > 128 {
		return errors.New("too many classified paths")
	}
	for i, p := range all {
		if !validPath(p) || p == "package.json" || p == r.Lockfile || p == "sandbox.yaml" {
			return errors.New("invalid classified path")
		}
		for _, q := range all[:i] {
			if p == q || strings.HasPrefix(p, q+"/") || strings.HasPrefix(q, p+"/") {
				return errors.New("overlapping path classifications")
			}
		}
	}
	return nil
}
func under(p string, roots []string) bool {
	for _, r := range roots {
		if p == r || strings.HasPrefix(p, r+"/") {
			return true
		}
	}
	return false
}
func exclusion(p string, r Recipe) string {
	if under(p, r.DataPaths) {
		return "persistent-data"
	}
	if under(p, r.SecretPaths) {
		return "secret"
	}
	if under(p, r.OutputPaths) {
		return "build-output"
	}
	for _, part := range strings.Split(p, "/") {
		switch part {
		case "node_modules", ".venv", "venv", "__pycache__":
			return "dependency"
		case ".git":
			return "git-history"
		}
	}
	return ""
}

// ExcludedPath lets a fenced filesystem exporter skip entire dependency/data
// trees before reading or compressing them. Call Recipe.Validate first.
func ExcludedPath(p string, r Recipe) string { return exclusion(p, r) }
func sensitive(p string) bool {
	for _, part := range strings.Split(strings.ToLower(p), "/") {
		if part == ".env" || strings.HasPrefix(part, ".env.") || part == ".npmrc" || part == ".pypirc" || part == ".ssh" || part == ".aws" || part == ".git-credentials" || part == "id_rsa" || part == "id_ed25519" {
			return true
		}
	}
	ext := strings.ToLower(path.Ext(p))
	return ext == ".db" || ext == ".sqlite" || ext == ".sqlite3" || ext == ".pem" || ext == ".key" || ext == ".p12"
}

type boundedBuffer struct{ bytes.Buffer }

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if len(p) > MaxBytes-b.Len() {
		return 0, errors.New("archive too large")
	}
	return b.Buffer.Write(p)
}

// Build requires a consistent input snapshot from the caller. It preserves
// private source, dotfiles, uncommitted files and executable bits. Classified
// data/secrets and reconstruction inputs are reported separately, never silently
// advertised as a complete owner backup. Unsupported links fail explicitly.
func Build(input []byte, r Recipe) ([]byte, Manifest, error) {
	m := Manifest{Version: 1, Kind: "private-project-source", Recipe: r, Files: []File{}, Excluded: []Exclusion{}}
	if err := r.Validate(); err != nil {
		return nil, m, err
	}
	if len(input) > MaxBytes {
		return nil, m, errors.New("input archive too large")
	}
	z, err := zip.NewReader(bytes.NewReader(input), int64(len(input)))
	if err != nil {
		return nil, m, err
	}
	if len(z.File) > MaxEntries {
		return nil, m, errors.New("too many entries")
	}
	files := append([]*zip.File{}, z.File...)
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	seen := map[string]bool{}
	regular := map[string]bool{}
	var total uint64
	for _, f := range files {
		p := strings.TrimSuffix(f.Name, "/")
		if !validPath(p) || seen[p] {
			return nil, m, errors.New("invalid or duplicate archive path")
		}
		seen[p] = true
		regular[p] = !f.FileInfo().IsDir()
		total += f.UncompressedSize64
		if f.UncompressedSize64 > MaxFileBytes || total > MaxBytes {
			return nil, m, errors.New("expanded archive too large")
		}
	}
	for p := range seen {
		for parent := path.Dir(p); parent != "."; parent = path.Dir(parent) {
			if regular[parent] {
				return nil, m, errors.New("file ancestor in archive")
			}
		}
	}
	var out boundedBuffer
	w := zip.NewWriter(&out)
	manifests := map[string][]byte{}
	for _, f := range files {
		p := strings.TrimSuffix(f.Name, "/")
		kind := exclusion(p, r)
		if kind != "" {
			m.Excluded = append(m.Excluded, Exclusion{p, kind})
			continue
		}
		if f.FileInfo().IsDir() {
			continue
		}
		if !f.Mode().IsRegular() {
			return nil, m, errors.New("source contains unsupported link or special file")
		}
		if sensitive(p) {
			return nil, m, fmt.Errorf("path requires data/secret classification: %s", p)
		}
		reader, e := f.Open()
		if e != nil {
			return nil, m, e
		}
		body, e := io.ReadAll(io.LimitReader(reader, MaxFileBytes+1))
		reader.Close()
		if e != nil || len(body) > MaxFileBytes || uint64(len(body)) != f.UncompressedSize64 {
			return nil, m, errors.New("source entry integrity failed")
		}
		h := &zip.FileHeader{Name: p, Method: zip.Deflate}
		mode := f.Mode().Perm() & 0111
		h.SetMode(0600 | mode)
		dst, e := w.CreateHeader(h)
		if e != nil {
			return nil, m, e
		}
		if _, e = dst.Write(body); e != nil {
			return nil, m, e
		}
		m.Files = append(m.Files, File{p, digest(body), len(body), mode != 0})
		if p == "package.json" || p == r.Lockfile {
			manifests[p] = body
		}
	}
	if len(m.Files) == 0 {
		return nil, m, errors.New("no source files")
	}
	if r.PackageManager != "none" {
		if len(manifests[r.Lockfile]) == 0 || len(manifests["package.json"]) == 0 {
			return nil, m, errors.New("manifest or lockfile missing")
		}
		if !json.Valid(manifests["package.json"]) {
			return nil, m, errors.New("invalid package.json")
		}
	} else {
		// A dependency-free recipe cannot silently omit an application's installer.
		for _, f := range m.Files {
			if f.Path == "package.json" || f.Path == "requirements.txt" || f.Path == "pyproject.toml" || f.Path == "Cargo.toml" || f.Path == "go.mod" {
				return nil, m, errors.New("project needs a reviewed dependency recipe")
			}
		}
	}
	if err = w.Close(); err != nil {
		return nil, m, err
	}
	m.Bytes = out.Len()
	m.SourceSHA256 = digest(out.Bytes())
	// Include all source hashes: local/workspace packages and dependency patches
	// influence installations even when the root lockfile is unchanged.
	key, _ := json.Marshal(struct {
		Runtime string
		Manager string
		Install []string
		Files   []File
	}{r.RuntimeImage, r.PackageManager, r.Install, m.Files})
	m.DependencyKey = digest(key)
	return out.Bytes(), m, nil
}

// Verify is required after downloading a source object, before guest extraction.
func Verify(data []byte, m Manifest) error {
	if m.Version != 1 || m.Kind != "private-project-source" || len(data) != m.Bytes || len(data) > MaxBytes || digest(data) != m.SourceSHA256 {
		return errors.New("source checksum or metadata differs")
	}
	_, actual, err := Build(data, m.Recipe)
	if err != nil {
		return err
	}
	a, _ := json.Marshal(actual.Files)
	b, _ := json.Marshal(m.Files)
	if !bytes.Equal(a, b) || actual.DependencyKey != m.DependencyKey || len(actual.Excluded) != 0 {
		return errors.New("source manifest differs")
	}
	return nil
}
