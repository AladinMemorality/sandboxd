package api

import (
	"context"
	"database/sql"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
	"testing"
)

func TestCubeGitRejectsUnreviewedDestinationBeforeFetch(t *testing.T) {
	s := &Server{}
	for _, raw := range []string{"https://127.0.0.1/repo", "https://metadata.internal/repo", "https://github.com:444/a/b", "https://github.com/a/b?token=secret"} {
		app := &store.App{GitRepoURL: sql.NullString{String: raw, Valid: true}, GitBranch: sql.NullString{String: "main", Valid: true}}
		if _, e := s.prepareCubeGit(context.Background(), app, "react-vite"); e == nil {
			t.Fatal("accepted " + raw)
		}
	}
}
func TestCubeGitCredentialCannotCrossOwner(t *testing.T) {
	s, _ := newConfigTestServer(t)
	app := &store.App{OwnerToken: "other-owner", GitRepoURL: sql.NullString{String: "https://github.com/org/repo", Valid: true}, GitBranch: sql.NullString{String: "main", Valid: true}, GitCredentialID: sql.NullString{String: "missing-credential", Valid: true}}
	if _, e := s.prepareCubeGit(context.Background(), app, "react-vite"); e == nil {
		t.Fatal("missing/other-owner credential accepted")
	}
}
