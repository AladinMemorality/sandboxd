package api

import (
	"context"
	"errors"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/gitimport"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/preset"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (s *Server) prepareCubeGit(ctx context.Context, app *store.App, presetID string) ([]byte, error) {
	if !app.GitRepoURL.Valid || app.GitRepoURL.String == "" {
		return nil, nil
	}
	if e := gitimport.ValidateReviewedRepoURL(app.GitRepoURL.String); e != nil {
		return nil, e
	}
	branch := app.GitBranch.String
	if branch == "" {
		branch = "main"
	}
	if e := gitimport.ValidateBranch(branch); e != nil {
		return nil, e
	}
	spec := gitimport.Spec{RepoURL: app.GitRepoURL.String, Branch: branch}
	if app.GitCredentialID.Valid && app.GitCredentialID.String != "" {
		if s.Secrets == nil {
			return nil, errors.New("credential store unavailable")
		}
		credentials, e := s.Store.ListGitCredentials(ctx, app.OwnerToken)
		if e != nil {
			return nil, e
		}
		u, _ := url.Parse(spec.RepoURL)
		found := false
		for _, credential := range credentials {
			if credential.ID == app.GitCredentialID.String {
				if credential.Host != "" && !strings.EqualFold(credential.Host, u.Hostname()) {
					return nil, errors.New("Git credential host does not match repository")
				}
				spec.Username = credential.Username
				found = true
				break
			}
		}
		if !found {
			return nil, errors.New("Git credential unavailable")
		}
		enc, nonce, found, e := s.Store.GetGitCredentialSecret(ctx, app.OwnerToken, app.GitCredentialID.String)
		if e != nil || !found {
			return nil, errors.New("Git credential unavailable")
		}
		token, e := s.Secrets.Open(enc, nonce)
		if e != nil {
			return nil, errors.New("Git credential unavailable")
		}
		defer func() {
			for i := range token {
				token[i] = 0
			}
		}()
		spec.Token = string(token)
	}
	dir, e := os.MkdirTemp("", "cube-git-")
	if e != nil {
		return nil, e
	}
	defer os.RemoveAll(dir)
	spec.DestDir = filepath.Join(dir, "app")
	if e = gitimport.CloneReviewed(ctx, spec); e != nil {
		return nil, errors.New("reviewed Git clone failed")
	}
	// A missing manifest inherits the explicitly selected preset. A present
	// manifest may not silently change the trusted template's routed port.
	manifest := filepath.Join(spec.DestDir, "sandbox.yaml")
	if info, e := os.Lstat(manifest); os.IsNotExist(e) {
		p, ok := preset.Get(presetID)
		if !ok {
			return nil, errors.New("unknown Git runtime preset")
		}
		if e = os.WriteFile(manifest, []byte(p.Manifest), 0644); e != nil {
			return nil, e
		}
	} else if e != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return nil, errors.New("invalid repository manifest")
	}
	if port := workspaceWebPort(spec.DestDir); port > 0 && port != presetWebPort(presetID) {
		return nil, errors.New("repository web port differs from the selected Cube template")
	}
	// Full archive is permitted only for this same owner's explicit Git import.
	// The separate publication path always sanitizes and excludes .git/data.
	return runtime.ExportPrivateWorkspaceContext(ctx, spec.DestDir)
}
func (s *Server) importCubeGit(ctx context.Context, id string, archive []byte) error {
	client := s.runtimeClientFor(id)
	before, e := client.Status(ctx)
	if e != nil {
		return e
	}
	if e = client.QuiesceWorkspace(ctx); e != nil {
		return e
	}
	if e = client.ImportGitWorkspace(ctx, archive); e != nil {
		return e
	}
	wait, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	for {
		status, e := client.Status(wait)
		if e == nil && status.Runtimed.BootedAt.After(before.Runtimed.BootedAt) {
			break
		}
		select {
		case <-wait.Done():
			return wait.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return client.ResumeWorkspace(ctx)
}
