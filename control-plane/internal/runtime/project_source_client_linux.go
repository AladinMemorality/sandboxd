package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/projectsource"
)

func (c *Client) ExportProjectRevision(ctx context.Context, recipe projectsource.Recipe) ([]byte, projectsource.Manifest, error) {
	var m projectsource.Manifest
	if err := recipe.Validate(); err != nil {
		return nil, m, err
	}
	raw, err := json.Marshal(recipe)
	if err != nil {
		return nil, m, err
	}
	resp, cleanup, err := c.workspaceTypedRequest(ctx, http.MethodPost, "/export/project-source", bytes.NewReader(raw), int64(len(raw)), "application/json")
	if err != nil {
		return nil, m, err
	}
	defer cleanup()
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, m, &ResponseError{StatusCode: resp.StatusCode}
	}
	media, params, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || media != "multipart/form-data" || params["boundary"] == "" {
		return nil, m, errors.New("invalid source response")
	}
	mr := multipart.NewReader(io.LimitReader(resp.Body, projectsource.MaxBytes+(2<<20)), params["boundary"])
	part, err := mr.NextPart()
	if err != nil || part.FormName() != "manifest" {
		return nil, m, errors.New("missing source manifest")
	}
	raw, err = io.ReadAll(io.LimitReader(part, (1<<20)+1))
	part.Close()
	if err != nil || len(raw) > 1<<20 {
		return nil, m, errors.New("invalid source manifest length")
	}
	if err = json.Unmarshal(raw, &m); err != nil {
		return nil, m, err
	}
	part, err = mr.NextPart()
	if err != nil || part.FormName() != "source" {
		return nil, m, errors.New("missing source archive")
	}
	data, err := io.ReadAll(io.LimitReader(part, projectsource.MaxBytes+1))
	part.Close()
	if err != nil {
		return nil, m, err
	}
	if _, err = mr.NextPart(); err != io.EOF {
		return nil, m, errors.New("unexpected source response data")
	}
	expected, _ := json.Marshal(recipe)
	actual, _ := json.Marshal(m.Recipe)
	if !bytes.Equal(expected, actual) {
		return nil, m, errors.New("export recipe changed")
	}
	if err = projectsource.Verify(data, m); err != nil {
		return nil, m, err
	}
	return data, m, nil
}

func (c *Client) ImportProjectRevision(ctx context.Context, data []byte, m projectsource.Manifest) error {
	if err := projectsource.Verify(data, m); err != nil {
		return err
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormField("manifest")
	if err != nil {
		return err
	}
	if err = json.NewEncoder(part).Encode(m); err != nil {
		return err
	}
	part, err = mw.CreateFormFile("source", "source.zip")
	if err != nil {
		return err
	}
	if _, err = part.Write(data); err != nil {
		return err
	}
	if err = mw.Close(); err != nil {
		return err
	}
	resp, cleanup, err := c.workspaceTypedRequest(ctx, http.MethodPut, "/import/project-source", &body, int64(body.Len()), mw.FormDataContentType())
	if err != nil {
		return err
	}
	defer cleanup()
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return &ResponseError{StatusCode: resp.StatusCode}
	}
	var receipt struct {
		Prepared bool   `json:"prepared"`
		SHA      string `json:"source_sha256"`
		Key      string `json:"dependency_key"`
		Quiesced bool   `json:"quiesced"`
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4097))
	if err != nil || len(raw) > 4096 {
		return errors.New("invalid import receipt")
	}
	if err = json.Unmarshal(raw, &receipt); err != nil {
		return err
	}
	if !receipt.Prepared || !receipt.Quiesced || receipt.SHA != m.SourceSHA256 || receipt.Key != m.DependencyKey {
		return errors.New("import identity verification failed")
	}
	return nil
}
