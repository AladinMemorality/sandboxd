package main

import (
	"strings"
	"testing"
)

func TestCleanReceiptCannotAcceptAbruptLossOrOtherGeneration(t *testing.T) {
	s := escrow{Fixture: "owned", BootID: "old"}
	r := cleanReceipt{Purpose: "DISPOSABLE_CUBE_CLEAN_REBOOT_COMPLETE", Fixture: "owned", PreviousBoot: "old", Method: "operator-reviewed-clean-powerdown"}
	if validateRebootReceipt(r, s, cleanMode) != nil {
		t.Fatal("valid clean receipt rejected")
	}
	for _, edit := range []func(*cleanReceipt){func(r *cleanReceipt) { r.Method = "operator-reviewed-power-loss" }, func(r *cleanReceipt) { r.Fixture = "other" }, func(r *cleanReceipt) { r.PreviousBoot = "new" }, func(r *cleanReceipt) { r.Purpose = "DISPOSABLE_CUBE_CRASH_COMPLETE" }} {
		v := r
		edit(&v)
		if validateRebootReceipt(v, s, cleanMode) == nil {
			t.Fatal("wrong receipt accepted")
		}
	}
}
func TestCleanInventoryRequiresCompleteSingleOwnedGuest(t *testing.T) {
	id := strings.Repeat("a", 32)
	good := "SANDBOX_COUNT 1\nNODES_SCANNED 1/1\n" + id + " paused\n"
	if !oneOwnedInventory(good, id) {
		t.Fatal("owned inventory rejected")
	}
	for _, v := range []string{strings.Replace(good, "1/1", "0/1", 1), strings.Replace(good, "COUNT 1", "COUNT 2", 1), strings.Replace(good, id, strings.Repeat("b", 32), 1), good + id + " paused\n"} {
		if oneOwnedInventory(v, id) {
			t.Fatal("ambiguous inventory accepted")
		}
	}
}

func TestEmptyInventoryRequiresCompleteNodeScan(t *testing.T) {
	if !emptyInventory("SANDBOX_COUNT 0\nNODES_SCANNED 1/1\n") {
		t.Fatal("empty complete inventory rejected")
	}
	for _, v := range []string{"SANDBOX_COUNT 0", "SANDBOX_COUNT 0\nNODES_SCANNED 0/1", "SANDBOX_COUNT 1\nNODES_SCANNED 1/1", "SANDBOX_COUNT 0\nSANDBOX_COUNT 0\nNODES_SCANNED 1/1"} {
		if emptyInventory(v) {
			t.Fatal("incomplete or nonempty inventory accepted")
		}
	}
}

func TestPausedLossAndCleanReceiptsNeverInterchange(t *testing.T) {
	s := escrow{Fixture: "owned", BootID: "old"}
	for _, mode := range []durabilityMode{cleanMode, pausedLossMode} {
		r := cleanReceipt{Purpose: mode.purpose(), Fixture: s.Fixture, PreviousBoot: s.BootID, Method: mode.method()}
		if validateRebootReceipt(r, s, mode) != nil {
			t.Fatal("own receipt rejected")
		}
		for _, other := range []durabilityMode{cleanMode, pausedLossMode, durabilityMode("unknown")} {
			if other != mode && validateRebootReceipt(r, s, other) == nil {
				t.Fatal("cross-mode receipt accepted")
			}
		}
	}
	if cleanMode.prefix() == pausedLossMode.prefix() || cleanMode.receipt() == pausedLossMode.receipt() || cleanMode.checkpoint() == pausedLossMode.checkpoint() || cleanMode.handoff() == pausedLossMode.handoff() {
		t.Fatal("mode artifacts collide")
	}
}
func TestModeMustBeExplicitAndKnown(t *testing.T) {
	for _, action := range []string{"run", "verify", "cleanup"} {
		got, mode, e := parseMode(action)
		if e != nil || got != action || mode != cleanMode {
			t.Fatal("clean default changed")
		}
		got, mode, e = parseMode(action + "-paused-loss")
		if e != nil || got != action || mode != pausedLossMode {
			t.Fatal("paused-loss mode missing")
		}
	}
	for _, v := range []string{"paused-loss", "run-running-loss", "run-paused-loss-extra", ""} {
		if _, _, e := parseMode(v); e == nil {
			t.Fatal("ambiguous mode accepted")
		}
	}
}
