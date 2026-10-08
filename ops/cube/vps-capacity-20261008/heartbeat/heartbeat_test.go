package store

import "testing"

func TestEqualJSONPreservesInventoryChanges(t *testing.T) {
	for _, tt := range []struct {
		a, b  string
		equal bool
	}{
		{`{"b":2,"a":1}`, `{ "a": 1, "b": 2 }`, true},
		{`{"id":9007199254740992}`, `{"id":9007199254740993}`, false},
		{`["first","second"]`, `["second","first"]`, false},
		{`null`, `[]`, false},
		{`{"ready":true}`, `{"ready":false}`, false},
		{`invalid`, `null`, false},
	} {
		if got := equalJSON(tt.a, tt.b); got != tt.equal {
			t.Errorf("equalJSON(%s,%s)=%v", tt.a, tt.b, got)
		}
	}
}

func TestInventoryOrderingDoesNotHideContentChanges(t *testing.T) {
	for _, tt := range []struct {
		a, b  string
		equal bool
	}{
		{`[{"id":"a","path":"x"},{"id":"b"}]`, `[{"id":"b"},{"path":"x","id":"a"}]`, true},
		{`[{"id":"a","path":"x"}]`, `[{"id":"a","path":"y"}]`, false},
		{`[{"id":"a"},{"id":"a"}]`, `[{"id":"a"}]`, false},
		{`[9007199254740992]`, `[9007199254740993]`, false},
		{`null`, `[]`, false},
	} {
		if got := equalInventoryJSON(tt.a, tt.b); got != tt.equal {
			t.Errorf("equalInventoryJSON(%s,%s)=%v", tt.a, tt.b, got)
		}
	}
}
