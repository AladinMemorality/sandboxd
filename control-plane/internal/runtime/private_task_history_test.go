package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const historyID1 = "01M2QTJAM3F62CJ6ZQMEPE8H22"
const historyID2 = "01M2QTJAYD0JCM4665XT28G5PJ"

func writeHistoryFixture(t *testing.T, root, id string) {
	t.Helper()
	dir := filepath.Join(root, id)
	if e := os.MkdirAll(dir, 0755); e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte("{\"type\":\"done\"}\n"), 0644)
	os.WriteFile(filepath.Join(dir, "result.json"), []byte(`{"id":"`+id+`","status":"succeeded","checkpoint_id":"abc123"}`), 0644)
	os.WriteFile(filepath.Join(dir, "agent.log"), []byte("RAW_SHOULD_NOT_COPY"), 0644)
	os.WriteFile(filepath.Join(dir, "stream.jsonl"), []byte("RAW_STREAM"), 0644)
}
func TestPrivateTaskHistorySelectedRoundtripMergeAndIdempotency(t *testing.T) {
	src := t.TempDir()
	writeHistoryFixture(t, src, historyID1)
	writeHistoryFixture(t, src, historyID2)
	data, e := ExportPrivateTaskHistory(context.Background(), src, []string{historyID1})
	if e != nil {
		t.Fatal(e)
	}
	ids, e := PrivateTaskHistoryIDs(data)
	if e != nil || len(ids) != 1 || ids[0] != historyID1 {
		t.Fatalf("selection %v %v", ids, e)
	}
	if strings.Contains(string(data), "agent.log") {
		t.Fatal("raw logs included")
	}
	before, _ := PrivateWorkspaceDigest(data)
	dst := filepath.Join(t.TempDir(), "tasks")
	os.Mkdir(dst, 0755)
	writeHistoryFixture(t, dst, historyID2)
	for i := 0; i < 2; i++ {
		if e = InstallPrivateTaskHistory(dst, data); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = os.Stat(filepath.Join(dst, historyID1, "agent.log")); !os.IsNotExist(e) {
		t.Fatal("raw agent log copied")
	}
	if _, e = os.Stat(filepath.Join(dst, historyID2, "result.json")); e != nil {
		t.Fatal("unselected task removed")
	}
	copy, e := ExportPrivateTaskHistory(context.Background(), dst, []string{historyID1})
	if e != nil {
		t.Fatal(e)
	}
	after, _ := PrivateWorkspaceDigest(copy)
	if before != after {
		t.Fatal("history checksum changed")
	}
	os.WriteFile(filepath.Join(dst, historyID1, "result.json"), []byte(`{"id":"`+historyID1+`","status":"failed"}`), 0644)
	if e = InstallPrivateTaskHistory(dst, data); e == nil {
		t.Fatal("conflicting history overwritten")
	}
}
func TestPrivateTaskHistoryRejectsPathIdentityAndFileAttacks(t *testing.T) {
	for _, id := range []string{"../other", "task1", strings.ToLower(historyID1), "8" + historyID1[1:]} {
		if ValidatePrivateTaskIDs([]string{id}) == nil {
			t.Fatal("accepted task ID " + id)
		}
	}
	src := t.TempDir()
	writeHistoryFixture(t, src, historyID1)
	os.WriteFile(filepath.Join(src, historyID1, "result.json"), []byte(`{"id":"`+historyID2+`","status":"succeeded"}`), 0644)
	if _, e := ExportPrivateTaskHistory(context.Background(), src, []string{historyID1}); e == nil {
		t.Fatal("wrong task result accepted")
	}
	os.WriteFile(filepath.Join(src, historyID1, "result.json"), []byte(`{"id":"`+historyID1+`","status":"running"}`), 0644)
	if _, e := ExportPrivateTaskHistory(context.Background(), src, []string{historyID1}); e == nil {
		t.Fatal("active task exported")
	}
	writeHistoryFixture(t, src, historyID1)
	os.Remove(filepath.Join(src, historyID1, "events.jsonl"))
	outside := filepath.Join(t.TempDir(), "secret")
	os.WriteFile(outside, []byte("secret"), 0600)
	os.Symlink(outside, filepath.Join(src, historyID1, "events.jsonl"))
	if _, e := ExportPrivateTaskHistory(context.Background(), src, []string{historyID1}); e == nil {
		t.Fatal("linked event log exported")
	}
	type entry = struct {
		name, body string
		mode       os.FileMode
	}
	for _, name := range []string{"../token", historyID1 + "/auth.json", historyID1 + "/../result.json", "supervisor-token"} {
		data := privateTestZip([]entry{{name, "secret", 0644}})
		if ValidatePrivateTaskHistoryArchive(data) == nil {
			t.Fatal("unexpected history filename accepted " + name)
		}
	}
}
func TestPrivateTaskHistoryEmptyMissingRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing/tasks")
	data, e := ExportPrivateTaskHistory(context.Background(), root, nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = ValidatePrivateTaskHistoryArchive(data); e != nil {
		t.Fatal(e)
	}
	if e = InstallPrivateTaskHistory(root, data); e != nil {
		t.Fatal(e)
	}
}
