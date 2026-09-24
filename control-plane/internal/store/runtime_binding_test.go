package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestRuntimeBindingAtomicAndPrivate(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	docker := &Sandbox{ID: "docker-default", Status: "stopped"}
	if err := st.Create(ctx, docker); err != nil {
		t.Fatal(err)
	}
	got, err := st.Get(ctx, docker.ID)
	if err != nil || got.RuntimeProvider != "docker" {
		t.Fatalf("default provider: %+v %v", got, err)
	}
	cube := &Sandbox{ID: "cube-stable-id", Status: "running", RuntimeProvider: "cube"}
	if err := st.Create(ctx, cube); err == nil {
		t.Fatal("accepted unbound Cube row")
	}
	if _, err := st.Get(ctx, cube.ID); err != ErrNotFound {
		t.Fatal("partial Cube row persisted")
	}
	cube.RuntimeBinding = &RuntimeBinding{Provider: "cube", RuntimeID: "remote-vm-id", TemplateID: "tpl-trusted", Domain: "cube.test", TokenCiphertext: []byte("secret-ciphertext"), TokenNonce: []byte("secret-nonce")}
	if err := st.Create(ctx, cube); err != nil {
		t.Fatal(err)
	}
	got, err = st.Get(ctx, cube.ID)
	if err != nil || got.RuntimeProvider != "cube" || got.ID == cube.RuntimeBinding.RuntimeID {
		t.Fatalf("bad stable binding: %+v %v", got, err)
	}
	b, err := st.GetRuntimeBinding(ctx, cube.ID)
	if err != nil || b.RuntimeID != "remote-vm-id" {
		t.Fatalf("binding: %+v %v", b, err)
	}
	for _, object := range []any{cube, b, got} {
		data, _ := json.Marshal(object)
		if strings.Contains(string(data), "Token") || strings.Contains(string(data), "secret") {
			t.Fatalf("secret exposed in JSON: %s", data)
		}
	}
	duplicate := &Sandbox{ID: "another-stable-id", Status: "running", RuntimeProvider: "cube", RuntimeBinding: cube.RuntimeBinding}
	if err := st.Create(ctx, duplicate); err == nil {
		t.Fatal("runtime reused across two owners")
	}
	if _, err := st.Get(ctx, duplicate.ID); err != ErrNotFound {
		t.Fatal("failed binding left row behind")
	}
	if err := st.Delete(ctx, cube.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetRuntimeBinding(ctx, cube.ID); err != ErrNotFound {
		t.Fatalf("orphan binding after delete: %v", err)
	}
}
