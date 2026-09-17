package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	MaxFileReadBytes        = 2 << 20
	MaxFileWriteBytes       = 25 << 20
	MaxWorkspaceExportBytes = 64 << 20
	MaxWorkspaceEntries     = 10000
	MaxProcessLogBytes      = 256 << 10
)

type FileEntry struct {
	Path string `json:"path"`
	Type string `json:"type"`
	Size int64  `json:"size,omitempty"`
}
type FileList struct {
	Path      string      `json:"path"`
	Recursive bool        `json:"recursive"`
	Entries   []FileEntry `json:"entries"`
}
type FileWrite struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}
type ProcessLog struct {
	Process string   `json:"process"`
	Lines   []string `json:"lines"`
}

// ResponseError carries only a status code; guest response bodies may contain
// secrets and are deliberately never reflected in control-plane error messages.
type ResponseError struct{ StatusCode int }

func (e *ResponseError) Error() string {
	return fmt.Sprintf("guest operation returned HTTP %d", e.StatusCode)
}

func (c *Client) bounded(ctx context.Context, method, path string, body []byte, cap int64) ([]byte, error) {
	hc := c.http
	if method == http.MethodPut || strings.HasPrefix(path, "/export") {
		// Transfer operations have bounded payloads and an explicit larger budget.
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		hc = c.stream
	}
	resp, err := c.do(ctx, hc, method, "http://runtimed"+path, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, &ResponseError{StatusCode: resp.StatusCode}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, cap+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > cap {
		return nil, errors.New("guest response exceeds limit")
	}
	return data, nil
}
func (c *Client) ListFiles(ctx context.Context, path string, recursive bool) (*FileList, error) {
	data, err := c.bounded(ctx, http.MethodGet, "/files?path="+url.QueryEscape(path)+"&recursive="+strconv.FormatBool(recursive), nil, MaxFileReadBytes)
	if err != nil {
		return nil, err
	}
	var out FileList
	err = json.Unmarshal(data, &out)
	return &out, err
}
func (c *Client) ReadFile(ctx context.Context, path string) ([]byte, error) {
	return c.bounded(ctx, http.MethodGet, "/files/content?path="+url.QueryEscape(path), nil, MaxFileReadBytes)
}
func (c *Client) PutFile(ctx context.Context, path string, body io.Reader) (*FileWrite, error) {
	data, err := io.ReadAll(io.LimitReader(body, MaxFileWriteBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxFileWriteBytes {
		return nil, &ResponseError{StatusCode: http.StatusRequestEntityTooLarge}
	}
	result, err := c.bounded(ctx, http.MethodPut, "/files?path="+url.QueryEscape(path), data, 4096)
	if err != nil {
		return nil, err
	}
	var out FileWrite
	err = json.Unmarshal(result, &out)
	return &out, err
}
func (c *Client) ExportWorkspace(ctx context.Context) ([]byte, error) {
	return c.bounded(ctx, http.MethodGet, "/export", nil, MaxWorkspaceExportBytes)
}
func (c *Client) ProcessLogs(ctx context.Context, name string, tail int) (*ProcessLog, error) {
	data, err := c.bounded(ctx, http.MethodGet, "/processes/"+url.PathEscape(name)+"/logs?tail="+strconv.Itoa(tail), nil, MaxFileReadBytes)
	if err != nil {
		return nil, err
	}
	var out ProcessLog
	err = json.Unmarshal(data, &out)
	return &out, err
}
func (c *Client) TaskResult(ctx context.Context, id string) (*TaskResult, error) {
	data, err := c.bounded(ctx, http.MethodGet, "/tasks/"+url.PathEscape(id)+"/result", nil, MaxFileReadBytes)
	if err != nil {
		return nil, err
	}
	var out TaskResult
	if err = json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	if out.ID != id || (out.Status != TaskSucceeded && out.Status != TaskFailed && out.Status != TaskCancelled) {
		return nil, errors.New("invalid guest task result")
	}
	return &out, nil
}
