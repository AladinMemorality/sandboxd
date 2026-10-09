package runtime

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPublishedDataCompatibilityPreservesExistingWork(t *testing.T) {
	for _, scenario := range []string{"missing", "same", "changed", "busy", "read-failure", "bad-receipt"} {
		t.Run(scenario, func(t *testing.T) {
			const name = "src/data/wedding.ts"
			const content = "export const couple = {};"
			writes := 0
			current := ""
			if scenario == "same" {
				current = content
			}
			if scenario == "changed" {
				current = "owner edit"
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/files/content":
					if r.URL.Query().Get("path") != name {
						t.Errorf("private source read: %s", r.URL.Query().Get("path"))
						w.WriteHeader(400)
						return
					}
					if scenario == "read-failure" {
						w.WriteHeader(503)
					} else if current == "" {
						w.WriteHeader(404)
					} else {
						io.WriteString(w, current)
					}
				case "/status":
					if scenario == "busy" {
						io.WriteString(w, `{"active_task":{"id":"owner-task"}}`)
					} else {
						io.WriteString(w, `{}`)
					}
				case "/files":
					if r.Method != http.MethodPut || r.URL.Query().Get("path") != name {
						t.Error("unexpected write")
						w.WriteHeader(400)
						return
					}
					writes++
					data, _ := io.ReadAll(r.Body)
					current = string(data)
					size := len(data)
					if scenario == "bad-receipt" {
						size--
					}
					json.NewEncoder(w).Encode(FileWrite{Path: name, Size: int64(size)})
				default:
					t.Error("unexpected endpoint")
					w.WriteHeader(404)
				}
			}))
			defer srv.Close()
			c, err := NewRemoteClient(RemoteConfig{BaseURL: srv.URL, Token: testRemoteToken, Host: "owner.cube.test", TrafficAccessToken: "private-ingress"})
			if err != nil {
				t.Fatal(err)
			}
			data := testSourceZip(t, map[string]string{name: content, "src/data/users.json": "PRIVATE", "src/data/secrets.ts": "PRIVATE"})
			err = c.EnsurePublishedDataModules(context.Background(), data)
			success := scenario == "missing" || scenario == "same"
			if (err == nil) != success {
				t.Fatalf("success=%v, error=%v", success, err)
			}
			wantWrites := 0
			if scenario == "missing" || scenario == "bad-receipt" {
				wantWrites = 1
			}
			if writes != wantWrites {
				t.Fatalf("writes=%d, want %d", writes, wantWrites)
			}
			if scenario == "changed" && current != "owner edit" {
				t.Fatal("owner edit overwritten")
			}
		})
	}
}

func TestPublishedDataExportRejectsUnstableInventory(t *testing.T) {
	for _, scenario := range []string{"changed-size", "duplicate", "too-large", "read-failure"} {
		t.Run(scenario, func(t *testing.T) {
			name := "src/data/wedding.ts"
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/export/source":
					w.Write(testSourceZip(t, map[string]string{"src/main.ts": "source"}))
				case "/files":
					entry := FileEntry{Path: name, Type: "file", Size: 2}
					if scenario == "too-large" {
						entry.Size = MaxFileWriteBytes + 1
					}
					entries := []FileEntry{entry}
					if scenario == "duplicate" {
						entries = append(entries, entry)
					}
					json.NewEncoder(w).Encode(FileList{Entries: entries})
				case "/files/content":
					if scenario == "too-large" {
						t.Error("oversized file read")
					}
					if scenario == "read-failure" {
						w.WriteHeader(503)
					} else if scenario == "changed-size" {
						io.WriteString(w, "changed")
					} else {
						io.WriteString(w, "ok")
					}
				default:
					t.Error("unexpected endpoint")
					w.WriteHeader(404)
				}
			}))
			defer srv.Close()
			c, err := NewRemoteClient(RemoteConfig{BaseURL: srv.URL, Token: testRemoteToken, Host: "owner.cube.test", TrafficAccessToken: "private-ingress"})
			if err != nil {
				t.Fatal(err)
			}
			_, err = c.ExportSource(context.Background())
			if err == nil {
				t.Fatal("unstable source accepted")
			}
			if strings.Contains(err.Error(), "PRIVATE") {
				t.Fatal("private response leaked")
			}
		})
	}
}
