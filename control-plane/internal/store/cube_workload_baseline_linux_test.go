package store_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
)

// Optional single retained canary; never passed to create/connect/delete. The
// wrapper binds these pins to the canonical paused row and private checkpoint.
// The ordinary empty-inventory behavior remains the default.
type workloadPausedBaseline struct {
	RuntimeID           string `json:"runtime_id"`
	TemplateID          string `json:"template_id"`
	AppID               string `json:"app_id"`
	ProviderJSONSHA256  string `json:"provider_json_sha256"`
	ConfigSHA256        string `json:"config_sha256"`
	MarkerReceiptSHA256 string `json:"marker_receipt_sha256"`
}

func (b *workloadPausedBaseline) validate() error {
	if len(b.RuntimeID) != 32 || len(b.AppID) != 26 || b.TemplateID == "" {
		return errors.New("invalid paused baseline identity")
	}
	if _, err := hex.DecodeString(b.RuntimeID); err != nil {
		return err
	}
	for _, value := range []string{b.ProviderJSONSHA256, b.ConfigSHA256, b.MarkerReceiptSHA256} {
		if len(value) != 64 {
			return errors.New("missing baseline evidence hash")
		}
		if _, err := hex.DecodeString(value); err != nil {
			return err
		}
	}
	return nil
}
func validateWorkloadInventory(rows []json.RawMessage, baseline *workloadPausedBaseline) error {
	if baseline == nil {
		if len(rows) != 0 {
			return errors.New("nonempty inventory")
		}
		return nil
	}
	if err := baseline.validate(); err != nil {
		return err
	}
	if len(rows) != 1 {
		return errors.New("unexpected provider guest")
	}
	var row map[string]any
	if json.Unmarshal(rows[0], &row) != nil {
		return errors.New("invalid provider row")
	}
	metadata, ok := row["metadata"].(map[string]any)
	if !ok || metadata["sandboxd_app_id"] != baseline.AppID || row["sandboxID"] != baseline.RuntimeID || row["templateID"] != baseline.TemplateID || row["state"] != "paused" || row["cpuCount"] != float64(2) || row["memoryMB"] != float64(2048) {
		return errors.New("paused provider identity changed")
	}
	canonical, err := json.Marshal(row)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(canonical)
	if hex.EncodeToString(sum[:]) != baseline.ProviderJSONSHA256 {
		return errors.New("paused provider fingerprint changed")
	}
	return nil
}
func TestWorkloadPausedBaselineIsExactAndOptional(t *testing.T) {
	raw := json.RawMessage(`{"sandboxID":"0123456789abcdef0123456789abcdef","templateID":"tpl-reviewed","state":"paused","cpuCount":2,"memoryMB":2048,"metadata":{"sandboxd_app_id":"01M3CZB4HXT2Y8HP8CEY75PCWY"}}`)
	var row map[string]any
	json.Unmarshal(raw, &row)
	canonical, _ := json.Marshal(row)
	sum := sha256.Sum256(canonical)
	hash := hex.EncodeToString(sum[:])
	b := &workloadPausedBaseline{RuntimeID: row["sandboxID"].(string), TemplateID: "tpl-reviewed", AppID: "01M3CZB4HXT2Y8HP8CEY75PCWY", ProviderJSONSHA256: hash, ConfigSHA256: hash, MarkerReceiptSHA256: hash}
	if validateWorkloadInventory([]json.RawMessage{raw}, b) != nil || validateWorkloadInventory(nil, nil) != nil {
		t.Fatal("valid baseline refused")
	}
	if validateWorkloadInventory([]json.RawMessage{raw}, nil) == nil || validateWorkloadInventory(nil, b) == nil || validateWorkloadInventory([]json.RawMessage{raw, raw}, b) == nil {
		t.Fatal("inventory mismatch accepted")
	}
	for key, value := range map[string]any{"state": "running", "templateID": "tpl-other", "sandboxID": "fedcba9876543210fedcba9876543210", "cpuCount": 4, "memoryMB": 4096} {
		old := row[key]
		row[key] = value
		changed, _ := json.Marshal(row)
		if validateWorkloadInventory([]json.RawMessage{changed}, b) == nil {
			t.Fatal("changed baseline accepted", key)
		}
		row[key] = old
	}
	row["metadata"] = map[string]any{"sandboxd_app_id": "another-owner"}
	changed, _ := json.Marshal(row)
	if validateWorkloadInventory([]json.RawMessage{changed}, b) == nil {
		t.Fatal("wrong owner accepted")
	}
}
