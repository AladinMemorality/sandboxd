package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	runtimeapi "github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/secrets"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func jsonBytes(v any) []byte {
	b, e := json.Marshal(v)
	if e != nil {
		panic(e)
	}
	return b
}
func sample(t *testing.T) (config, map[string][]byte, expected) {
	t.Helper()
	c := config{Version: 1, Generation: "/opt/baarcha-cube/backup-generations/test", RuntimeID: strings.Repeat("1", 32), TemplateID: "tpl-reviewed", Domain: "cube.app", OwnerTokenSHA256: digest([]byte("owner")), ConfigRevision: 1, Files: map[string]string{}}
	c.CloneRoot = c.Generation + "/clone/root.qcow2"
	c.CloneData = c.Generation + "/clone/data.qcow2"
	sources := map[string][]byte{}
	ref := func(name string, b []byte) reference {
		p := c.Generation + "/" + name
		sources[p] = b
		return reference{p, digest(b)}
	}
	c.Database = ref("state.db", []byte("placeholder"))
	c.Key = ref("key", []byte("placeholder"))
	done := map[string]any{}
	for _, p := range []string{"sandbox.yaml", "server.mjs", "public/index.html", ".operator-recovery/" + fixtureRun + "/home-worker.mjs", ".operator-recovery/" + fixtureRun + "/app marker #.txt"} {
		c.Files[p] = digest([]byte(p))
		done["write:"+p] = map[string]string{"sha256": c.Files[p]}
	}
	proof := map[string]any{"run": fixtureRun, "uid": 1000, "home_sha256": digest([]byte("operator-recovery-" + fixtureRun + ":home")), "app_sha256": digest([]byte("operator-recovery-" + fixtureRun + ":app")), "hardlink_same_inode": true, "hardlink_count": 2, "file_mode": 0640, "script_mode": 0750, "home_file": "home marker\n# [1].txt", "symlink_target": "home marker\n# [1].txt", "ai_success": false}
	row := map[string]any{"id": 1, "body": "operator-recovery-" + fixtureRun + ":acknowledged-row", "created_at": "2026-09-25T22:27:38.800Z"}
	j := map[string]any{"run": fixtureRun, "app": appID, "sandbox": sandboxID, "pending": nil, "manifest_restored": true, "verified": map[string]any{"at": "2026-09-25T22:27:41.220Z", "source_data_only": true, "ai_coding_passed": false}, "source_proof": proof, "sql_row": row, "before": map[string]any{"sandbox.yaml": map[string]string{"sha256": c.Files["sandbox.yaml"]}}, "done": done}
	c.OperatorJournal = ref("operator.json", jsonBytes(j))
	for i := 0; i < 4; i++ {
		id := fmt.Sprintf("01M3D1Q0E1KM1FEM244XVHEC6%d", i)
		result := runtimeapi.TaskResult{ID: id, Status: runtimeapi.TaskFailed, FailureReason: "operator_fixture_preserves_failure"}
		full := struct {
			Sandbox string `json:"sandbox_id"`
			runtimeapi.TaskResult
		}{sandboxID, result}
		c.Tasks = append(c.Tasks, taskReceipt{id, ref(id+".json", jsonBytes(full)), ref(id+".events", []byte(fmt.Sprintf("id: 0\nevent: status\ndata: {}\n\nid: 1\nevent: done\ndata: %s\n\n", jsonBytes(result))))})
	}
	e, err := sourceExpected(c, func(r reference) ([]byte, error) {
		b, ok := sources[r.Path]
		if !ok || digest(b) != r.SHA256 {
			return nil, failed
		}
		return b, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return c, sources, e
}
func TestSourceExpectationsRejectMissingOrAlteredProof(t *testing.T) {
	for _, change := range []string{"pending", "uid", "hardlink", "mode", "ai", "sql", "manifest", "events", "wrongtask"} {
		t.Run(change, func(t *testing.T) {
			c, s, _ := sample(t)
			var j map[string]any
			_ = json.Unmarshal(s[c.OperatorJournal.Path], &j)
			switch change {
			case "pending":
				j["pending"] = map[string]string{"name": "ambiguous"}
			case "uid":
				j["source_proof"].(map[string]any)["uid"] = 0
			case "hardlink":
				j["source_proof"].(map[string]any)["hardlink_count"] = 1
			case "mode":
				j["source_proof"].(map[string]any)["file_mode"] = 0644
			case "ai":
				j["verified"].(map[string]any)["ai_coding_passed"] = true
			case "sql":
				j["sql_row"].(map[string]any)["body"] = "different"
			case "manifest":
				j["manifest_restored"] = false
			case "events":
				s[c.Tasks[0].Events.Path] = []byte("id: 0\nevent: status\ndata: {}\n\n")
			case "wrongtask":
				c.Tasks[0].ID = c.Tasks[1].ID
			}
			s[c.OperatorJournal.Path] = jsonBytes(j)
			_, err := sourceExpected(c, func(r reference) ([]byte, error) { return s[r.Path], nil })
			if err == nil {
				t.Fatal("accepted altered evidence")
			}
		})
	}
}
func TestConfigurationRefusesProductionPathsAndAmbiguousIdentity(t *testing.T) {
	c, _, _ := sample(t)
	if validate(c) != nil {
		t.Fatal("baseline")
	}
	for _, change := range []func(*config){func(c *config) { c.Database.Path = "/var/lib/sandboxd/state/sandboxd.db" }, func(c *config) { c.Key.Path = "/var/lib/sandboxd/secrets.key" }, func(c *config) { c.Domain = "attacker.example" }, func(c *config) { c.CloneRoot = "/opt/baarcha-cube/worker-01/root.qcow2" }, func(c *config) { c.Tasks[0].ID = c.Tasks[1].ID }, func(c *config) { c.Tasks = c.Tasks[:3] }, func(c *config) { c.Generation += "/../live" }} {
		x, _, _ := sample(t)
		change(&x)
		if validate(x) == nil {
			t.Fatal("unsafe config accepted")
		}
	}
}
func database(t *testing.T, c config, e expected) (config, []byte) {
	t.Helper()
	c.Database.Path = filepath.Join(t.TempDir(), "restored.db")
	db, err := sql.Open("sqlite3", c.Database.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	schema := `CREATE TABLE app(id TEXT,owner_token TEXT,external_user_id TEXT,external_project_id TEXT,runtime_preset TEXT);CREATE TABLE sandbox(id TEXT,app_id TEXT,runtime_provider TEXT);CREATE TABLE runtime_binding(sandbox_id TEXT,provider TEXT,runtime_id TEXT,template_id TEXT,domain TEXT,config_revision INTEGER,config_applied_revision INTEGER,token_ciphertext BLOB,token_nonce BLOB);CREATE TABLE task(task_id TEXT,sandbox_id TEXT,status TEXT,result_json TEXT);CREATE TABLE cube_admission(state TEXT);CREATE TABLE cube_recovery(phase TEXT);`
	if _, err = db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	key := []byte(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{42}, 32)))
	cipher, _ := secrets.Load(string(key), "")
	encrypted, nonce, _ := cipher.Seal(jsonBytes(credentials{strings.Repeat("a", 64), "private-traffic-token"}))
	statements := []struct {
		q string
		a []any
	}{{`INSERT INTO app VALUES(?,?,?,?,?)`, []any{appID, "owner", "baarcha:103", "cube-recovery:6ff432593177fb12", "node-postgres"}}, {`INSERT INTO sandbox VALUES(?,?,?)`, []any{sandboxID, appID, "cube"}}, {`INSERT INTO runtime_binding VALUES(?,?,?,?,?,?,?,?,?)`, []any{sandboxID, "cube", c.RuntimeID, c.TemplateID, c.Domain, 1, 1, encrypted, nonce}}}
	for _, s := range statements {
		if _, err = db.Exec(s.q, s.a...); err != nil {
			t.Fatal(err)
		}
	}
	for id, r := range e.results {
		if _, err = db.Exec(`INSERT INTO task VALUES(?,?,?,?)`, id, sandboxID, "failed", string(jsonBytes(r))); err != nil {
			t.Fatal(err)
		}
	}
	return c, key
}
func TestCopiedSQLiteCredentialsAndTaskResultsAreVerifiedWithoutWrites(t *testing.T) {
	c, _, e := sample(t)
	c, key := database(t, c, e)
	before, _ := os.ReadFile(c.Database.Path)
	cred, err := readDB(context.Background(), c, e, key)
	if err != nil || cred.Supervisor != strings.Repeat("a", 64) {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(c.Database.Path)
	if !bytes.Equal(before, after) {
		t.Fatal("database changed")
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err = os.Stat(c.Database.Path + suffix); !os.IsNotExist(err) {
			t.Fatal("sqlite side effect", suffix)
		}
	}
	if _, err = readDB(context.Background(), c, e, []byte(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))); err == nil {
		t.Fatal("wrong key accepted")
	}
}
func TestCopiedSQLiteRejectsIdentityConfigHistoryAndPendingDrift(t *testing.T) {
	mutations := []string{`UPDATE app SET external_user_id='baarcha:104'`, `UPDATE app SET owner_token='different'`, `UPDATE sandbox SET runtime_provider='docker'`, `UPDATE runtime_binding SET runtime_id='wrong'`, `UPDATE runtime_binding SET config_applied_revision=0`, `UPDATE runtime_binding SET token_ciphertext=X'00'`, `UPDATE task SET status='running'`, `UPDATE task SET result_json='{}'`, `INSERT INTO cube_admission VALUES('pending')`, `INSERT INTO cube_recovery VALUES('prepared')`, `DELETE FROM task WHERE rowid=1`}
	for _, q := range mutations {
		t.Run(q, func(t *testing.T) {
			c, _, e := sample(t)
			c, key := database(t, c, e)
			db, _ := sql.Open("sqlite3", c.Database.Path)
			_, err := db.Exec(q)
			db.Close()
			if err != nil {
				t.Fatal(err)
			}
			if _, err = readDB(context.Background(), c, e, key); err == nil {
				t.Fatal("accepted drift")
			}
		})
	}
}
func responses(c config, e expected) map[string][]byte {
	out := map[string][]byte{"S/status": jsonBytes(map[string]any{"preview": map[string]string{"status": "ready"}, "active_task": nil, "processes": []map[string]any{{"name": "web", "running": true, "pid": 4}, {"name": "postgres", "running": true, "pid": 3}}}), "W/operator-recovery-proof": e.proof, "W/health": []byte(`{"status":"ok"}`), "W/api/notes": append(append([]byte{'['}, e.row...), ']'), "W/": []byte("operator-recovery-" + fixtureRun + ":app")}
	for p := range c.Files {
		out["S/files/content?path="+urlEscape(p)] = []byte(p)
	}
	for _, r := range c.Tasks {
		out["S/tasks/"+r.ID+"/result"] = jsonBytes(e.results[r.ID])
		var b []byte
		for _, v := range e.events[r.ID] {
			b = append(b, jsonBytes(v)...)
			b = append(b, '\n')
		}
		out["S/tasks/"+r.ID+"/events?since=0"] = b
	}
	return out
}
func urlEscape(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(s, "/", "%2F"), " ", "+"), "#", "%23"), "\n", "%0A")
}
func TestActualResponseComparisonRequiresFilesHistoryHomeAndSQL(t *testing.T) {
	c, _, e := sample(t)
	out := responses(c, e)
	get := func(ctx context.Context, s bool, p string, max int64) ([]byte, error) {
		prefix := "W"
		if s {
			prefix = "S"
		}
		b, ok := out[prefix+p]
		if !ok || int64(len(b)) > max {
			return nil, failed
		}
		return b, nil
	}
	if err := verifyGuest(context.Background(), c, e, get); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"S/status", "S/files/content?path=server.mjs", "S/tasks/" + c.Tasks[0].ID + "/events?since=0", "S/tasks/" + c.Tasks[0].ID + "/result", "W/operator-recovery-proof", "W/api/notes", "W/health", "W/"} {
		original := out[key]
		out[key] = []byte(`{}`)
		if verifyGuest(context.Background(), c, e, get) == nil {
			t.Fatal("false pass", key)
		}
		out[key] = original
	}
	out["W/api/notes"] = []byte("[" + string(e.row) + "," + string(e.row) + "]")
	if verifyGuest(context.Background(), c, e, get) == nil {
		t.Fatal("duplicate SQL row passed")
	}
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestTransportIsReadOnlyAndScopesSupervisorCredential(t *testing.T) {
	c, _, _ := sample(t)
	cred := credentials{strings.Repeat("a", 64), "traffic"}
	calls := 0
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "GET" || r.URL.Scheme+"://"+r.URL.Host != cloneProxy || r.Header.Get("cube-traffic-access-token") != cred.Traffic {
			t.Fatal("unsafe request")
		}
		if strings.HasPrefix(r.Host, "3031-") {
			if r.Header.Get("Authorization") != "Bearer "+cred.Supervisor {
				t.Fatal("no supervisor auth")
			}
		} else if r.Header.Get("Authorization") != "" {
			t.Fatal("supervisor bearer leaked to app")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok")), Header: http.Header{}}, nil
	})}
	guardCalls := 0
	get := networkGetter(c, cred, func() error { guardCalls++; return nil }, client)
	for _, s := range []bool{true, false} {
		if _, err := get(context.Background(), s, "/", 4); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 || guardCalls != 4 {
		t.Fatal("guard not checked around each read")
	}
	if _, err := get(context.Background(), true, "/", 1); err == nil {
		t.Fatal("truncated oversized response accepted")
	}
	get = networkGetter(c, cred, func() error { return failed }, client)
	before := calls
	if _, err := get(context.Background(), true, "/", 4); err == nil || calls != before {
		t.Fatal("guard failure reached network")
	}
}
func TestEventParserRejectsTruncationReplayAndMissingTerminal(t *testing.T) {
	c, s, e := sample(t)
	raw := s[c.Tasks[0].Events.Path]
	valid, err := parseSSE(raw)
	if err != nil || checkEvents(valid, e.results[c.Tasks[0].ID]) != nil {
		t.Fatal("baseline")
	}
	for _, b := range [][]byte{raw[:len(raw)-1], append(raw, raw...), []byte("id: 0\nevent: done\ndata: {}\n\n"), []byte("id: 0\nid: 0\nevent: status\ndata: {}\n\n")} {
		events, err := parseSSE(b)
		if err == nil && checkEvents(events, e.results[c.Tasks[0].ID]) == nil {
			t.Fatal("invalid events accepted")
		}
	}
}
