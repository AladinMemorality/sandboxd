package store

import (
	"context"
	"os"
	"strings"
	"testing"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// Run only against a disposable schema. It must have MINIMAL binary logging
// enabled so this verifies the actual writes rather than a mocked SQL string.
func TestHeartbeatMySQLInventoryAndLogVolume(t *testing.T) {
	dsn := os.Getenv("CUBE_HEARTBEAT_TEST_DSN")
	if dsn == "" {
		t.Skip("disposable MySQL required")
	}
	if !strings.Contains(dsn, "/cube_heartbeat_test?") {
		t.Fatal("dedicated test schema required")
	}
	db, e := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if e != nil {
		t.Fatal(e)
	}
	sqlDB, e := db.DB()
	if e != nil {
		t.Fatal(e)
	}
	defer sqlDB.Close()
	if e = db.AutoMigrate(&NodeRegistration{}, &NodeStatus{}); e != nil {
		t.Fatal(e)
	}
	if e = db.Exec("ALTER TABLE t_cube_node_status MODIFY node_id VARCHAR(128) NOT NULL").Error; e != nil {
		t.Fatal(e)
	}
	if e = db.Exec("CREATE UNIQUE INDEX heartbeat_node_id ON t_cube_node_status(node_id)").Error; e != nil {
		t.Fatal(e)
	}
	s := NewNodeStore(db)
	ctx := context.Background()
	reg := &NodeRegistration{NodeID: "fixture", HostFactsJSON: `{"b":2,"a":1}`, CPUIDHash: "cpu", HostKernelRelease: "kernel"}
	if e = db.Create(reg).Error; e != nil {
		t.Fatal(e)
	}
	old, e := s.GetRegistration(ctx, "fixture")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.UpdateHostFacts(ctx, "fixture", `{ "a": 1, "b": 2 }`, "cpu", "kernel"); e != nil {
		t.Fatal(e)
	}
	unchanged, e := s.GetRegistration(ctx, "fixture")
	if e != nil || !unchanged.UpdatedAt.Equal(old.UpdatedAt) {
		t.Fatal("unchanged facts generated a write", e)
	}
	if e = s.UpdateHostFacts(ctx, "fixture", `{"a":2}`, "cpu2", "kernel2"); e != nil {
		t.Fatal(e)
	}
	updated, e := s.GetRegistration(ctx, "fixture")
	if e != nil || updated.CPUIDHash != "cpu2" || !equalJSON(updated.HostFactsJSON, `{"a":2}`) {
		t.Fatal("changed facts lost", e)
	}
	inventory := `{"images":["` + strings.Repeat("x", 100000) + `"]}`
	shuffledInventory := func(n int64) string {
		a := `{"id":"a","path":"` + strings.Repeat("y", 100000) + `"}`
		b := `{"id":"b"}`
		if n%2 == 0 {
			return "[" + a + "," + b + "]"
		}
		return "[" + b + "," + a + "]"
	}
	status := func(n int64, healthy bool) *NodeStatus {
		return &NodeStatus{NodeID: "fixture", ConditionsJSON: `{"ready":true}`, ImagesJSON: inventory, LocalTemplatesJSON: shuffledInventory(n), HeartbeatUnix: n, Healthy: healthy}
	}
	if e = s.UpsertStatus(ctx, status(1, true)); e != nil {
		t.Fatal(e)
	}
	type position struct {
		File     string
		Position uint64
	}
	var before, after position
	if e = db.Raw("SHOW MASTER STATUS").Scan(&before).Error; e != nil || before.Position == 0 {
		t.Fatal("binary log required", e)
	}
	for i := int64(2); i <= 101; i++ {
		if e = s.UpsertStatus(ctx, status(i, i%2 == 0)); e != nil {
			t.Fatal(e)
		}
	}
	if e = db.Raw("SHOW MASTER STATUS").Scan(&after).Error; e != nil {
		t.Fatal(e)
	}
	if after.File != before.File || after.Position < before.Position || after.Position-before.Position > 100000 {
		t.Fatalf("unchanged inventory amplified logs: %v -> %v", before, after)
	}
	got, e := s.GetStatus(ctx, "fixture")
	if e != nil || got.HeartbeatUnix != 101 || got.Healthy || !equalJSON(got.ImagesJSON, inventory) {
		t.Fatal("liveness or inventory lost", e)
	}
	changed := status(102, false)
	changed.ImagesJSON = `[]`
	changed.ConditionsJSON = `{"ready":false}`
	if e = s.UpsertStatus(ctx, changed); e != nil {
		t.Fatal(e)
	}
	got, e = s.GetStatus(ctx, "fixture")
	if e != nil || got.HeartbeatUnix != 102 || !equalJSON(got.ImagesJSON, `[]`) || !equalJSON(got.ConditionsJSON, changed.ConditionsJSON) {
		t.Fatal("inventory update lost", e)
	}
	t.Logf("100 heartbeats with a 100KB inventory wrote %d binary-log bytes", after.Position-before.Position)
}
