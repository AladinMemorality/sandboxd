package wake

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/idlock"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

func TestManagedPreviewDoesNotReachDockerAdmission(t *testing.T) {
	t.Setenv("SANDBOXD_EXPLICIT_WAKE_PREFIXES", "baarcha:")
	st, err := store.Open(context.Background(), "file:"+filepath.Join(t.TempDir(), "test.db")+"?_fk=1", "../../migrations")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	const id = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	if err = st.Create(context.Background(), &store.Sandbox{ID: id, Status: "stopped", Image: "test", RuntimeProvider: "docker", Visibility: "public", ExternalUserID: sql.NullString{String: "baarcha:1", Valid: true}}); err != nil {
		t.Fatal(err)
	}
	// No Docker client or capacity reader: reaching either is a test failure.
	h, err := New(st, nil, "example.test", Config{}, AdmitConfig{}, nil, idlock.New(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "http://s-"+id+"-3000.preview.example.test/", nil)
	w := httptest.NewRecorder()
	h.ServeCatchAll(w, r)
	if w.Code != 409 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	row, err := st.Get(context.Background(), id)
	if err != nil || row.Status != "stopped" {
		t.Fatalf("sandbox changed: %v %v", row, err)
	}
}
