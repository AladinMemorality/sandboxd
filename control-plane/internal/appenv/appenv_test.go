package appenv

import (
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

func TestFormat(t *testing.T) {
	open := func(ct, nonce []byte) ([]byte, error) {
		if string(ct) == "bad" {
			return nil, errors.New("tampered")
		}
		return []byte("sk-" + string(ct)), nil
	}
	rows := []*store.AppConfig{
		{Key: "OPENAI_API_KEY", Sensitive: true, ValueCiphertext: []byte("live"), AccessPolicy: "runtime_access"},
		{Key: "APP_NAME", ValuePlaintext: sql.NullString{String: "Faith Mask", Valid: true}, AccessPolicy: "both"},
		{Key: "HIDDEN", Sensitive: true, ValueCiphertext: []byte("x"), AccessPolicy: "control_plane_only"},
		{Key: "AGENT_ONLY", Sensitive: true, ValueCiphertext: []byte("x"), AccessPolicy: "agent_access"},
		{Key: "not a name", ValuePlaintext: sql.NullString{String: "v", Valid: true}, AccessPolicy: "both"},
		{Key: "BROKEN", Sensitive: true, ValueCiphertext: []byte("bad"), AccessPolicy: "runtime_access"},
		{Key: "EMPTY", AccessPolicy: "runtime_access"},
	}
	env, skipped := Format(rows, open)
	want := []string{"APP_NAME=Faith Mask", "OPENAI_API_KEY=sk-live"}
	if !reflect.DeepEqual(env, want) {
		t.Fatalf("env = %v, want %v", env, want)
	}
	if skipped != 1 {
		t.Fatalf("skipped = %d, want 1 (the undecryptable row)", skipped)
	}
}

func TestFormatWithoutCipher(t *testing.T) {
	rows := []*store.AppConfig{
		{Key: "SECRET", Sensitive: true, ValueCiphertext: []byte("x"), AccessPolicy: "runtime_access"},
		{Key: "PLAIN", ValuePlaintext: sql.NullString{String: "1", Valid: true}, AccessPolicy: "runtime_access"},
	}
	env, skipped := Format(rows, nil)
	if !reflect.DeepEqual(env, []string{"PLAIN=1"}) || skipped != 1 {
		t.Fatalf("env = %v skipped = %d", env, skipped)
	}
}
