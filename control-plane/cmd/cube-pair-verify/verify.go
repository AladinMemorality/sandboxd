package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
	runtimeapi "github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/secrets"
)

const appID = "01M3CZB4HXT2Y8HP8CEY75PCWY"
const sandboxID = "01M3D1Q0E1KM1FEM244XVHEC65"
const fixtureRun = "8bb03c01d5527213"
const cloneProxy = "http://127.0.0.1:21080"

var hashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
var taskPattern = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`)
var providerPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
var failed = errors.New("restored proof mismatch")

type reference struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}
type processPin struct {
	PID           int    `json:"pid"`
	Start         string `json:"start_ticks"`
	Exe           string `json:"exe"`
	CommandSHA256 string `json:"cmdline_sha256"`
}
type taskReceipt struct {
	ID     string    `json:"id"`
	Result reference `json:"result"`
	Events reference `json:"events"`
}
type config struct {
	Version          int               `json:"version"`
	Generation       string            `json:"generation"`
	Database         reference         `json:"database"`
	Key              reference         `json:"key"`
	OperatorJournal  reference         `json:"operator_journal"`
	RuntimeID        string            `json:"runtime_id"`
	TemplateID       string            `json:"template_id"`
	Domain           string            `json:"domain"`
	OwnerTokenSHA256 string            `json:"owner_token_sha256"`
	ConfigRevision   int64             `json:"config_revision"`
	Files            map[string]string `json:"files"`
	Tasks            []taskReceipt     `json:"tasks"`
	Clone            processPin        `json:"clone"`
	Proxy            processPin        `json:"proxy"`
	CloneRoot        string            `json:"clone_root"`
	CloneData        string            `json:"clone_data"`
	LockFDs          map[string]int    `json:"lock_fds"`
	WorkerLockFD     int               `json:"worker_lock_fd"`
}
type credentials struct {
	Supervisor string `json:"supervisor_token"`
	Traffic    string `json:"traffic_access_token"`
}
type expected struct {
	proof   json.RawMessage
	row     json.RawMessage
	results map[string]runtimeapi.TaskResult
	events  map[string][]event
}
type event struct {
	ID   int             `json:"id"`
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

func digest(b []byte) string { d := sha256.Sum256(b); return hex.EncodeToString(d[:]) }
func decode(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	if e := d.Decode(v); e != nil {
		return e
	}
	if d.Decode(new(any)) != io.EOF {
		return failed
	}
	return nil
}
func equalJSON(a, b []byte) bool {
	var x, y any
	return decode(a, &x) == nil && decode(b, &y) == nil && reflect.DeepEqual(x, y)
}
func validate(c config) error {
	if c.Version != 1 || !strings.HasPrefix(c.Generation, "/opt/baarcha-cube/backup-generations/") || filepath.Clean(c.Generation) != c.Generation || c.Generation == "/opt/baarcha-cube/backup-generations" || !providerPattern.MatchString(c.RuntimeID) || !strings.HasPrefix(c.TemplateID, "tpl-") || c.Domain != "cube.app" || !hashPattern.MatchString(c.OwnerTokenSHA256) || c.ConfigRevision < 0 || len(c.Tasks) != 4 {
		return failed
	}
	required := []string{"sandbox.yaml", "server.mjs", "public/index.html", ".operator-recovery/" + fixtureRun + "/home-worker.mjs", ".operator-recovery/" + fixtureRun + "/app marker #.txt"}
	if len(c.Files) != len(required) {
		return failed
	}
	for _, p := range required {
		if !hashPattern.MatchString(c.Files[p]) {
			return failed
		}
	}
	seen := map[string]bool{}
	for _, t := range c.Tasks {
		if !taskPattern.MatchString(t.ID) || seen[t.ID] {
			return failed
		}
		seen[t.ID] = true
	}
	for _, r := range appendRefs(c) {
		if !hashPattern.MatchString(r.SHA256) || !strings.HasPrefix(r.Path, c.Generation+"/") || filepath.Clean(r.Path) != r.Path {
			return failed
		}
	}
	for _, p := range []string{c.CloneRoot, c.CloneData} {
		if !strings.HasPrefix(p, c.Generation+"/") || filepath.Clean(p) != p {
			return failed
		}
	}
	if c.CloneRoot == c.CloneData {
		return failed
	}
	return nil
}
func appendRefs(c config) []reference {
	out := []reference{c.Database, c.Key, c.OperatorJournal}
	for _, t := range c.Tasks {
		out = append(out, t.Result, t.Events)
	}
	return out
}
func sourceExpected(c config, read func(reference) ([]byte, error)) (expected, error) {
	e := expected{results: map[string]runtimeapi.TaskResult{}, events: map[string][]event{}}
	b, err := read(c.OperatorJournal)
	if err != nil {
		return e, err
	}
	var j struct {
		Run              string
		App              string
		Sandbox          string
		Pending          any
		ManifestRestored bool `json:"manifest_restored"`
		Verified         struct {
			At             string
			SourceDataOnly bool `json:"source_data_only"`
			AICodingPassed bool `json:"ai_coding_passed"`
		}
		Proof  json.RawMessage `json:"source_proof"`
		Row    json.RawMessage `json:"sql_row"`
		Before map[string]struct{ SHA256 string }
		Done   map[string]struct{ SHA256 string }
	}
	if decode(b, &j) != nil || j.Run != fixtureRun || j.App != appID || j.Sandbox != sandboxID || j.Pending != nil || !j.ManifestRestored || j.Verified.At == "" || !j.Verified.SourceDataOnly || j.Verified.AICodingPassed || len(j.Proof) == 0 || len(j.Row) == 0 {
		return e, failed
	}
	if j.Before["sandbox.yaml"].SHA256 != c.Files["sandbox.yaml"] {
		return e, failed
	}
	for p, h := range c.Files {
		if p != "sandbox.yaml" && j.Done["write:"+p].SHA256 != h {
			return e, failed
		}
	}
	var proof struct {
		Run        string
		UID        int
		HomeSHA    string `json:"home_sha256"`
		AppSHA     string `json:"app_sha256"`
		Hard       bool   `json:"hardlink_same_inode"`
		Links      int    `json:"hardlink_count"`
		FileMode   int    `json:"file_mode"`
		ScriptMode int    `json:"script_mode"`
		HomeFile   string `json:"home_file"`
		Link       string `json:"symlink_target"`
		AI         bool   `json:"ai_success"`
	}
	if decode(j.Proof, &proof) != nil || proof.Run != fixtureRun || proof.UID != 1000 || !proof.Hard || proof.Links != 2 || proof.FileMode != 0640 || proof.ScriptMode != 0750 || proof.AI || proof.HomeFile != "home marker\n# [1].txt" || proof.Link != proof.HomeFile || proof.HomeSHA != digest([]byte("operator-recovery-"+fixtureRun+":home")) || proof.AppSHA != digest([]byte("operator-recovery-"+fixtureRun+":app")) {
		return e, failed
	}
	var row struct {
		ID      int
		Body    string
		Created string `json:"created_at"`
	}
	if decode(j.Row, &row) != nil || row.ID <= 0 || row.Body != "operator-recovery-"+fixtureRun+":acknowledged-row" {
		return e, failed
	}
	if _, err := time.Parse(time.RFC3339Nano, row.Created); err != nil {
		return e, failed
	}
	e.proof = j.Proof
	e.row = j.Row
	for _, t := range c.Tasks {
		b, err = read(t.Result)
		if err != nil {
			return e, err
		}
		var result struct {
			Sandbox string `json:"sandbox_id"`
			runtimeapi.TaskResult
		}
		if decode(b, &result) != nil || result.Sandbox != sandboxID || result.ID != t.ID || result.Status != runtimeapi.TaskFailed {
			return e, failed
		}
		e.results[t.ID] = result.TaskResult
		b, err = read(t.Events)
		if err != nil {
			return e, err
		}
		events, err := parseSSE(b)
		if err != nil {
			return e, err
		}
		if err = checkEvents(events, result.TaskResult); err != nil {
			return e, err
		}
		e.events[t.ID] = events
	}
	return e, nil
}
func parseSSE(b []byte) ([]event, error) {
	if len(b) > 16<<20 || bytes.Contains(b, []byte("\r")) {
		return nil, failed
	}
	s := bufio.NewScanner(bytes.NewReader(b))
	s.Buffer(make([]byte, 4096), 2<<20)
	var out []event
	cur := event{}
	fields := map[string]bool{}
	for s.Scan() {
		line := s.Text()
		if line == "" {
			if len(fields) != 3 {
				return nil, failed
			}
			out = append(out, cur)
			cur = event{}
			fields = map[string]bool{}
			continue
		}
		k, v, ok := strings.Cut(line, ": ")
		if !ok || fields[k] {
			return nil, failed
		}
		fields[k] = true
		switch k {
		case "id":
			n, e := strconv.Atoi(v)
			if e != nil {
				return nil, failed
			}
			cur.ID = n
		case "event":
			cur.Type = v
		case "data":
			if !json.Valid([]byte(v)) {
				return nil, failed
			}
			cur.Data = json.RawMessage(v)
		default:
			return nil, failed
		}
	}
	if s.Err() != nil || len(fields) != 0 || len(out) == 0 {
		return nil, failed
	}
	return out, nil
}
func checkEvents(events []event, result runtimeapi.TaskResult) error {
	if len(events) == 0 {
		return failed
	}
	last := -1
	done := 0
	for i, e := range events {
		if e.ID <= last || e.Type == "" || !json.Valid(e.Data) {
			return failed
		}
		last = e.ID
		if e.Type == "done" {
			done++
			var r runtimeapi.TaskResult
			if i != len(events)-1 || decode(e.Data, &r) != nil || !reflect.DeepEqual(r, result) {
				return failed
			}
		}
	}
	if done != 1 {
		return failed
	}
	return nil
}
func readDB(ctx context.Context, c config, e expected, key []byte) (credentials, error) {
	var cred credentials
	// A closed SQLite backup is required; immutable is safe only with the caller's
	// no-WAL/no-writer/hash checks. Never store.Open: no migration/bootstrap writes.
	u := url.URL{Scheme: "file", Path: c.Database.Path}
	q := u.Query()
	q.Set("mode", "ro")
	q.Set("immutable", "1")
	q.Set("_query_only", "1")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite3", u.String())
	if err != nil {
		return cred, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return cred, err
	}
	defer tx.Rollback()
	var check string
	if tx.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&check) != nil || check != "ok" {
		return cred, failed
	}
	var owner, user, project, preset, app, provider string
	err = tx.QueryRowContext(ctx, `SELECT a.owner_token,a.external_user_id,a.external_project_id,a.runtime_preset,s.app_id,s.runtime_provider FROM app a JOIN sandbox s ON s.app_id=a.id WHERE a.id=? AND s.id=?`, appID, sandboxID).Scan(&owner, &user, &project, &preset, &app, &provider)
	if err != nil || digest([]byte(owner)) != c.OwnerTokenSHA256 || user != "baarcha:103" || project != "cube-recovery:6ff432593177fb12" || preset != "node-postgres" || app != appID || provider != "cube" {
		return cred, failed
	}
	for _, query := range []string{`SELECT count(*) FROM runtime_binding`, `SELECT count(*) FROM task WHERE sandbox_id='` + sandboxID + `'`, `SELECT count(*) FROM task WHERE status='running'`, `SELECT count(*) FROM cube_admission WHERE state='pending'`, `SELECT count(*) FROM cube_recovery WHERE phase<>'complete'`} {
		var n int
		if tx.QueryRowContext(ctx, query).Scan(&n) != nil {
			return cred, failed
		}
		want := 0
		if strings.Contains(query, "runtime_binding") {
			want = 1
		}
		if strings.Contains(query, "sandbox_id=") {
			want = 4
		}
		if n != want {
			return cred, failed
		}
	}
	var runtimeID, template, domain, p string
	var rev, applied int64
	var ciphertext, nonce []byte
	err = tx.QueryRowContext(ctx, `SELECT provider,runtime_id,template_id,domain,config_revision,config_applied_revision,token_ciphertext,token_nonce FROM runtime_binding WHERE sandbox_id=?`, sandboxID).Scan(&p, &runtimeID, &template, &domain, &rev, &applied, &ciphertext, &nonce)
	if err != nil || p != "cube" || runtimeID != c.RuntimeID || template != c.TemplateID || domain != c.Domain || rev != c.ConfigRevision || applied != rev {
		return cred, failed
	}
	for id, want := range e.results {
		var status, raw, sb string
		if tx.QueryRowContext(ctx, `SELECT sandbox_id,status,result_json FROM task WHERE task_id=?`, id).Scan(&sb, &status, &raw) != nil || sb != sandboxID || status != "failed" {
			return cred, failed
		}
		var got runtimeapi.TaskResult
		if decode([]byte(raw), &got) != nil || !reflect.DeepEqual(got, want) {
			return cred, failed
		}
	}
	if len(bytes.TrimSpace(key)) == 0 {
		return cred, failed
	}
	cipher, err := secrets.Load(string(key), "")
	if err != nil {
		return cred, failed
	}
	plain, err := cipher.Open(ciphertext, nonce)
	if err != nil {
		return cred, failed
	}
	defer clear(plain)
	if decode(plain, &cred) != nil || runtimeapi.ValidateRemoteToken(cred.Supervisor) != nil || cred.Traffic == "" || len(cred.Traffic) > 4096 || strings.IndexFunc(cred.Traffic, func(r rune) bool { return r < 0x21 || r > 0x7e }) >= 0 {
		return credentials{}, failed
	}
	return cred, nil
}

type getter func(context.Context, bool, string, int64) ([]byte, error)

func verifyGuest(ctx context.Context, c config, e expected, get getter) error {
	b, err := get(ctx, true, "/status", 2<<20)
	if err != nil {
		return err
	}
	var status runtimeapi.Status
	if decode(b, &status) != nil || status.ActiveTask != nil || status.Preview.Status != runtimeapi.PreviewReady || len(status.Processes) < 2 {
		return failed
	}
	for _, p := range status.Processes {
		if !p.Running || p.Pid <= 0 {
			return failed
		}
	}
	names := make([]string, 0, len(c.Files))
	for p := range c.Files {
		names = append(names, p)
	}
	sort.Strings(names)
	for _, p := range names {
		b, err = get(ctx, true, "/files/content?path="+url.QueryEscape(p), 2<<20)
		if err != nil || digest(b) != c.Files[p] {
			return failed
		}
	}
	for _, t := range c.Tasks {
		b, err = get(ctx, true, "/tasks/"+t.ID+"/result", 2<<20)
		if err != nil {
			return err
		}
		var result runtimeapi.TaskResult
		if decode(b, &result) != nil || !reflect.DeepEqual(result, e.results[t.ID]) {
			return failed
		}
		b, err = get(ctx, true, "/tasks/"+t.ID+"/events?since=0", 16<<20)
		if err != nil {
			return err
		}
		var events []event
		d := json.NewDecoder(bytes.NewReader(b))
		for {
			var ev event
			err = d.Decode(&ev)
			if err == io.EOF {
				break
			}
			if err != nil {
				return failed
			}
			events = append(events, ev)
		}
		if checkEvents(events, result) != nil || len(events) != len(e.events[t.ID]) {
			return failed
		}
		for i, v := range events {
			w := e.events[t.ID][i]
			if v.ID != w.ID || v.Type != w.Type || !equalJSON(v.Data, w.Data) {
				return failed
			}
		}
	}
	b, err = get(ctx, false, "/operator-recovery-proof", 1<<20)
	if err != nil || !equalJSON(b, e.proof) {
		return failed
	}
	b, err = get(ctx, false, "/health", 1<<20)
	var health struct{ Status string }
	if err != nil || decode(b, &health) != nil || health.Status != "ok" {
		return failed
	}
	b, err = get(ctx, false, "/api/notes", 2<<20)
	if err != nil {
		return err
	}
	var notes []json.RawMessage
	if decode(b, &notes) != nil {
		return failed
	}
	matches := 0
	for _, row := range notes {
		if equalJSON(row, e.row) {
			matches++
		}
	}
	if matches != 1 {
		return failed
	}
	b, err = get(ctx, false, "/", 2<<20)
	if err != nil || !bytes.Contains(b, []byte("operator-recovery-"+fixtureRun+":app")) {
		return failed
	}
	return nil
}
func networkGetter(c config, cred credentials, guard func() error, client *http.Client) getter {
	return func(ctx context.Context, supervisor bool, path string, max int64) ([]byte, error) {
		if ctx.Err() != nil {
			return nil, failed
		}
		if err := guard(); err != nil {
			return nil, err
		}
		if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
			return nil, failed
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, cloneProxy+path, nil)
		if err != nil {
			return nil, failed
		}
		port := "3000"
		if supervisor {
			port = "3031"
			req.Header.Set("Authorization", "Bearer "+cred.Supervisor)
		}
		req.Host = fmt.Sprintf("%s-%s.%s", port, c.RuntimeID, c.Domain)
		req.Header.Set("cube-traffic-access-token", cred.Traffic)
		r, err := client.Do(req)
		if err != nil {
			return nil, failed
		}
		defer r.Body.Close()
		if r.StatusCode != http.StatusOK {
			return nil, failed
		}
		b, err := io.ReadAll(io.LimitReader(r.Body, max+1))
		if err != nil || int64(len(b)) > max {
			return nil, failed
		}
		return b, guard()
	}
}
