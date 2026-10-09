package runtime

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
)

// Old template supervisors excluded data directories during source export and
// import. Keep compatibility in the controller while those immutable templates
// age out. Only modules allowed by the current publishing policy qualify.
func publishedDataModule(name string) bool {
	if !ValidArchivePath(name) || !PublishedSourcePath(name) {
		return false
	}
	parts := strings.Split(name, "/")
	for _, part := range parts[:len(parts)-1] {
		if strings.EqualFold(part, "data") {
			return true
		}
	}
	return false
}

func (c *Client) completePublishedDataArchive(ctx context.Context, archive []byte) ([]byte, error) {
	clean, err := SanitizeSourceArchive(archive)
	if err != nil {
		return nil, err
	}
	z, err := zip.NewReader(bytes.NewReader(clean), int64(len(clean)))
	if err != nil {
		return nil, err
	}
	files, err := c.ListFiles(ctx, "", true)
	if err != nil {
		return nil, err
	}
	if len(files.Entries) > MaxWorkspaceEntries {
		return nil, errors.New("source inventory exceeds limit")
	}
	seen := map[string]bool{}
	var total uint64
	var out archiveBuffer
	writer := zip.NewWriter(&out)
	for _, file := range z.File {
		seen[file.Name] = true
		total += file.UncompressedSize64
		if err = writer.Copy(file); err != nil {
			return nil, err
		}
	}
	inventory := map[string]bool{}
	for _, file := range files.Entries {
		if file.Type != "file" || !publishedDataModule(file.Path) {
			continue
		}
		if inventory[file.Path] {
			return nil, errors.New("duplicate source inventory path")
		}
		inventory[file.Path] = true
		if seen[file.Path] {
			continue
		}
		if file.Size < 0 || file.Size > MaxFileWriteBytes || total+uint64(file.Size) > MaxWorkspaceExportBytes || len(seen) >= MaxWorkspaceEntries {
			return nil, errors.New("source inventory exceeds limit")
		}
		data, err := c.ReadFile(ctx, file.Path)
		if err != nil {
			return nil, err
		}
		if int64(len(data)) != file.Size {
			return nil, errors.New("source changed during export")
		}
		entry, err := writer.Create(file.Path)
		if err != nil {
			return nil, err
		}
		if _, err = entry.Write(data); err != nil {
			return nil, err
		}
		total += uint64(len(data))
		seen[file.Path] = true
	}
	if err = writer.Close(); err != nil {
		return nil, err
	}
	return SanitizeSourceArchive(out.Bytes())
}

// Verify after the import's supervisor restart, before reporting creation or
// restore success. Never overwrite a file changed by concurrent owner activity.
func (c *Client) EnsurePublishedDataModules(ctx context.Context, archive []byte) error {
	clean, err := SanitizeSourceArchive(archive)
	if err != nil {
		return err
	}
	z, err := zip.NewReader(bytes.NewReader(clean), int64(len(clean)))
	if err != nil {
		return err
	}
	for _, file := range z.File {
		if !publishedDataModule(file.Name) {
			continue
		}
		reader, err := file.Open()
		if err != nil {
			return err
		}
		want, err := io.ReadAll(io.LimitReader(reader, MaxFileWriteBytes+1))
		reader.Close()
		if err != nil || len(want) > MaxFileWriteBytes {
			return errors.New("invalid source module")
		}
		got, err := c.ReadFile(ctx, file.Name)
		if err == nil {
			if !bytes.Equal(got, want) {
				return errors.New("imported source module differs")
			}
			continue
		}
		var response *ResponseError
		if !errors.As(err, &response) || response.StatusCode != 404 {
			return err
		}
		status, err := c.Status(ctx)
		if err != nil {
			return err
		}
		if status.ActiveTask != nil {
			return errors.New("task started during source import")
		}
		receipt, err := c.PutFile(ctx, file.Name, bytes.NewReader(want))
		if err != nil {
			return err
		}
		if receipt.Path != file.Name || receipt.Size != int64(len(want)) {
			return errors.New("invalid source write receipt")
		}
		got, err = c.ReadFile(ctx, file.Name)
		if err != nil {
			return err
		}
		if !bytes.Equal(got, want) {
			return errors.New("source module verification failed")
		}
	}
	return nil
}
