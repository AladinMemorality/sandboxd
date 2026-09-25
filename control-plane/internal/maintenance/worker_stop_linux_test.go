package maintenance

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkerStopMarkerNeverExpiresOrOverwrites(t *testing.T) {
	db := filepath.Join(t.TempDir(), "state.db")
	if e := os.WriteFile(db, []byte("fixture"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := CheckWorkerStop(db); e != nil {
		t.Fatal(e)
	}
	if e := WriteWorkerStop(db, map[string]string{"phase": "draining", "created_at": "1900-01-01"}); e != nil {
		t.Fatal(e)
	}
	if e := CheckWorkerStop(db); e == nil {
		t.Fatal("old marker expired")
	}
	if e := WriteWorkerStop(db, map[string]string{"phase": "new"}); e == nil {
		t.Fatal("unfinished stop overwritten")
	}
	path, _ := WorkerStopMarker(db)
	info, e := os.Stat(path)
	if e != nil || info.Mode().Perm() != 0600 {
		t.Fatal("unsafe marker permissions")
	}
	os.WriteFile(path, nil, 0600)
	if e := CheckWorkerStop(db); e == nil {
		t.Fatal("partial marker ignored")
	}
}
func TestWorkerStopRejectsSymlinkDB(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "real")
	os.WriteFile(db, nil, 0600)
	link := filepath.Join(dir, "link")
	os.Symlink(db, link)
	if e := WriteWorkerStop(link, struct{}{}); e == nil {
		t.Fatal("symlink accepted")
	}
}
func TestWorkerStopClearExactGenerationOnly(t *testing.T) {
	db := filepath.Join(t.TempDir(), "db")
	os.WriteFile(db, nil, 0600)
	value := map[string]string{"generation": "old"}
	if e := WriteWorkerStop(db, value); e != nil {
		t.Fatal(e)
	}
	if ClearWorkerStop(db, map[string]string{"generation": "new"}) == nil {
		t.Fatal("wrong generation cleared")
	}
	if CheckWorkerStop(db) == nil {
		t.Fatal("marker vanished")
	}
	if e := ClearWorkerStop(db, value); e != nil {
		t.Fatal(e)
	}
	if e := CheckWorkerStop(db); e != nil {
		t.Fatal(e)
	}
}
