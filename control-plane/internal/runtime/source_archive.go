package runtime

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"path"
	"strings"
)

// PublishedSourcePath permits source/assets, not a user's home directory or
// runtime state. Publishing is an explicit decision to share these files: no
// filename policy can detect credentials hardcoded in otherwise valid source.
func PublishedSourcePath(name string) bool {
	parts := strings.Split(name, "/")
	for _, part := range parts {
		lower := strings.ToLower(part)
		if strings.HasPrefix(lower, ".") && lower != ".gitignore" && lower != ".dockerignore" && lower != ".well-known" {
			return false
		}
		switch lower {
		case "node_modules", "dist", "build", "out", "coverage", "venv", "__pycache__", "data", "storage", "uploads", "private", "secrets", "credentials", "logs", "tmp", "temp", "instance", "sessions":
			return false
		}
		if strings.HasPrefix(lower, "id_rsa") || strings.HasPrefix(lower, "id_ed25519") {
			return false
		}
	}
	base := strings.ToLower(path.Base(name))
 ext := strings.ToLower(path.Ext(name))
 // Authentication/session modules are source code, not automatically runtime
 // state. Keep the same JSON/data exclusions and explicit secret-file names.
 authModule := false
 switch ext { case ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".py", ".go", ".rb", ".rs", ".php": authModule=true }
	for _, prefix := range []string{"credentials.", "secrets.", "secret.", "tokens.", "token.", "auth.", "session.", "sessions.", "bench-state.", "runtime-state."} {
		if strings.HasPrefix(base, prefix) {
 if authModule && (prefix=="auth." || prefix=="session." || prefix=="sessions.") { continue }
 return false
		}
	}
	if base == "state.json" || base == "database.json" || base == "users.json" {
		return false
	}
	if ext == ".json" || ext == ".jsonc" {
		if len(parts) == 1 {
			switch base {
			case "package.json", "package-lock.json", "npm-shrinkwrap.json", "tsconfig.json", "jsconfig.json", "components.json", "vercel.json", "netlify.json", "composer.json", "deno.json", "deno.jsonc", "biome.json", "eslint.config.json":
			default:
				if !(strings.HasPrefix(base, "tsconfig.") && strings.HasSuffix(base, ".json")) {
					return false
				}
			}
		} else if !sourceAssetTree(parts[0]) {
			return false
		}
	}
	if ext == ".txt" && len(parts) == 1 && !strings.HasPrefix(base, "readme") && !strings.HasPrefix(base, "license") && !strings.HasPrefix(base, "requirements") && base != "robots.txt" && base != "cmakelists.txt" {
		return false
	}

	switch ext {
	case ".db", ".sqlite", ".sqlite3", ".db3", ".wal", ".shm", ".log", ".pem", ".key", ".p12", ".pfx", ".keystore", ".sql", ".dump", ".bak":
		return false
	}
	// The platform delivers owner-selected media into public/media. Preserve
	// those published assets, while documents/recordings elsewhere remain
	// private. The exclusions above still apply inside a public asset tree.
	switch ext {
	case ".mp4", ".webm", ".mov", ".mp3", ".m4a", ".wav", ".ogg", ".weba", ".flac", ".pdf":
		if len(parts) < 2 {
			return false
		}
		switch strings.ToLower(parts[0]) {
		case "public", "assets", "static":
			return true
		}
		return false
	}
	switch ext {
	case ".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs", ".json", ".jsonc", ".html", ".css", ".scss", ".sass", ".less", ".svg", ".png", ".jpg", ".jpeg", ".webp", ".gif", ".ico", ".avif", ".woff", ".woff2", ".ttf", ".otf", ".md", ".mdx", ".txt", ".yaml", ".yml", ".toml", ".py", ".rb", ".go", ".mod", ".sum", ".rs", ".lock", ".php", ".vue", ".svelte", ".astro", ".sh":
		return true
	}
	switch strings.ToLower(path.Base(name)) {
	case "dockerfile", "makefile", "license", "readme", "procfile", "gemfile", ".gitignore", ".dockerignore":
		return true
	}
	return false
}
func ValidArchivePath(name string) bool {
	if name == "" || len(name) > 4096 || strings.HasPrefix(name, "/") || strings.ContainsAny(name, "\x00\\") {
		return false
	}
	parts := strings.Split(name, "/")
	if len(parts) > 32 {
		return false
	}
	for _, p := range parts {
		if p == "" || p == "." || p == ".." {
			return false
		}
	}
	return true
}

type archiveBuffer struct{ bytes.Buffer }

func (b *archiveBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > MaxWorkspaceExportBytes {
		return 0, errors.New("source archive exceeds limit")
	}
	return b.Buffer.Write(p)
}

// SanitizeSourceArchive validates all ZIP paths/types/sizes before rebuilding
// the archive from the permitted source set. Limits cover compressed bytes,
// uncompressed bytes, individual files, and entry count; duplicate paths fail.
func SanitizeSourceArchive(data []byte) ([]byte, error) {
	if len(data) > MaxWorkspaceExportBytes {
		return nil, errors.New("source archive exceeds limit")
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, errors.New("invalid source archive")
	}
	if len(zr.File) > MaxWorkspaceEntries {
		return nil, errors.New("source archive exceeds entry limit")
	}
	seen := map[string]bool{}
	var total uint64
	var out archiveBuffer
	writer := zip.NewWriter(&out)
	count := 0
	for _, f := range zr.File {
		name := strings.TrimSuffix(f.Name, "/")
		if !ValidArchivePath(name) || seen[name] {
			return nil, errors.New("invalid or duplicate source archive path")
		}
		seen[name] = true
		mode := f.Mode()
		if mode.IsDir() {
			continue
		}
		if !mode.IsRegular() {
			return nil, errors.New("source archive contains special file")
		}
		if f.UncompressedSize64 > MaxFileWriteBytes {
			return nil, errors.New("source file exceeds limit")
		}
		total += f.UncompressedSize64
		if total > MaxWorkspaceExportBytes {
			return nil, errors.New("expanded source archive exceeds limit")
		}
		if !PublishedSourcePath(name) {
			continue
		}
		reader, err := f.Open()
		if err != nil {
			return nil, errors.New("invalid source file")
		}
		content, err := io.ReadAll(io.LimitReader(reader, MaxFileWriteBytes+1))
		reader.Close()
		if err != nil || len(content) > MaxFileWriteBytes || uint64(len(content)) != f.UncompressedSize64 {
			return nil, errors.New("invalid source file content")
		}
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetMode(0644)
		dst, err := writer.CreateHeader(header)
		if err != nil {
			return nil, err
		}
		if _, err = dst.Write(content); err != nil {
			return nil, err
		}
		count++
	}
	if count == 0 {
		return nil, errors.New("source archive has no publishable files")
	}
	if err = writer.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func (c *Client) ExportSource(ctx context.Context) ([]byte, error) {
	return c.bounded(ctx, http.MethodGet, "/export/source", nil, MaxWorkspaceExportBytes)
}
func (c *Client) ImportSource(ctx context.Context, data []byte) error {
	if len(data) > MaxWorkspaceExportBytes {
		return errors.New("source archive exceeds limit")
	}
	_, err := c.bounded(ctx, http.MethodPut, "/import/source", data, 4096)
	return err
}

func sourceAssetTree(root string) bool {
	switch strings.ToLower(root) {
	case "src", "public", "assets", "static", "app", "pages", "components", "lib", "server", "api", "config", "configs", "test", "tests", "fixtures", "packages", "apps":
		return true
	}
	return false
}
