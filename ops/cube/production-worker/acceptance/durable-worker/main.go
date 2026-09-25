// Owned clean pause/reboot acceptance. No power, repair or restore operation.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
)

type cleanReceipt struct {
	Purpose      string `json:"purpose"`
	Fixture      string `json:"fixture"`
	PreviousBoot string `json:"previous_boot_id"`
	Method       string `json:"method"`
}

type durabilityMode string

const cleanMode durabilityMode = "clean-pause-reboot"
const pausedLossMode durabilityMode = "acknowledged-pause-abrupt-loss"

func (m durabilityMode) purpose() string {
	if m == pausedLossMode {
		return "DISPOSABLE_CUBE_PAUSED_LOSS_COMPLETE"
	}
	return "DISPOSABLE_CUBE_CLEAN_REBOOT_COMPLETE"
}
func (m durabilityMode) handoff() string {
	if m == pausedLossMode {
		return "DISPOSABLE_CUBE_PAUSED_LOSS_HANDOFF"
	}
	return "DISPOSABLE_CUBE_CLEAN_HANDOFF"
}
func (m durabilityMode) method() string {
	if m == pausedLossMode {
		return "operator-reviewed-paused-power-loss"
	}
	return "operator-reviewed-clean-powerdown"
}
func (m durabilityMode) prefix() string {
	if m == pausedLossMode {
		return "/opt/baarcha-bench/cube-crash-paused-loss-"
	}
	return "/opt/baarcha-bench/cube-crash-clean-"
}
func (m durabilityMode) receipt() string {
	if m == pausedLossMode {
		return "paused-loss-complete.json"
	}
	return "clean-reboot-complete.json"
}
func (m durabilityMode) checkpoint() string {
	if m == pausedLossMode {
		return "PAUSED_LOSS_READY"
	}
	return "CLEAN_REBOOT_READY"
}
func expectedHandoffPurpose(c *coordinator) string {
	mode, ok := c.report["acceptance_mode"].(string)
	if !ok || (mode != string(cleanMode) && mode != string(pausedLossMode)) {
		panic("invalid durability mode")
	}
	return durabilityMode(mode).handoff()
}
func parseMode(value string) (string, durabilityMode, error) {
	mode := cleanMode
	action := value
	if strings.HasSuffix(value, "-paused-loss") {
		mode = pausedLossMode
		action = strings.TrimSuffix(value, "-paused-loss")
	}
	if action != "run" && action != "verify" && action != "cleanup" {
		return "", "", errors.New("invalid durability action")
	}
	return action, mode, nil
}
func validateRebootReceipt(r cleanReceipt, s escrow, mode durabilityMode) error {
	if (mode != cleanMode && mode != pausedLossMode) || r.Purpose != mode.purpose() || r.Fixture != s.Fixture || r.PreviousBoot != s.BootID || r.Method != mode.method() {
		return errors.New("exact mode-specific reboot receipt required")
	}
	return nil
}
func oneOwnedInventory(value, id string) bool {
	count := regexp.MustCompile(`(?m)^\s*SANDBOX_COUNT\s+(\d+)\s*$`).FindAllStringSubmatch(value, -1)
	nodes := regexp.MustCompile(`(?m)^\s*NODES_SCANNED\s+1/1\s*$`).FindAllString(value, -1)
	if len(count) != 1 || count[0][1] != "1" || len(nodes) != 1 {
		return false
	}
	found := 0
	for _, line := range strings.Split(value, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[0] == id {
			found++
		}
	}
	return found == 1
}
func emptyInventory(value string) bool {
	counts := regexp.MustCompile(`(?m)^\s*SANDBOX_COUNT\s+(\d+)\s*$`).FindAllStringSubmatch(value, -1)
	nodes := regexp.MustCompile(`(?m)^\s*NODES_SCANNED\s+1/1\s*$`).FindAllString(value, -1)
	return len(counts) == 1 && counts[0][1] == "0" && len(nodes) == 1
}
func cleanPause(c *coordinator, name string) {
	c.ownership(c.ctx)
	c.detach()
	start := time.Now()
	must(c.cube.Pause(c.ctx, c.saved.Guest.SandboxID))
	actual := c.ownership(c.ctx)
	if actual.State != "paused" {
		panic("pause not authoritative")
	}
	c.report[name+"_milliseconds"] = time.Since(start).Milliseconds()
	inv, e := worker(c.ctx, "cubemastercli -a 127.0.0.1 list --all --wide")
	must(e)
	if !oneOwnedInventory(inv, c.saved.Guest.SandboxID) {
		panic("unexpected worker inventory")
	}
	tasks, e := worker(c.ctx, "timeout 5s ctr --address /data/cubelet/cubelet.sock --namespace default tasks list --quiet")
	must(e)
	if tasks != "" {
		panic("owned pause left active Cube tasks")
	}
}
func cleanResume(c *coordinator, name string) {
	if c.ownership(c.ctx).State != "paused" {
		panic("expected paused exact original guest")
	}
	start := time.Now()
	_, e := c.cube.Connect(c.ctx, c.saved.Guest.SandboxID, cube.ConnectRequest{TimeoutSeconds: 1800})
	must(e)
	c.remote()
	c.attach()
	c.ready()
	c.probe("/verify", c.saved.Latest)
	if c.ownership(c.ctx).State != "running" {
		panic("resume state not authoritative")
	}
	c.report[name+"_through_sql_verify_milliseconds"] = time.Since(start).Milliseconds()
	c.report[name+"_latest_app_home_sql_preserved"] = true
}
func verifyDurability(c *coordinator, mode durabilityMode) {
	var r cleanReceipt
	must(privateJSON(filepath.Join(c.stage, mode.receipt()), &r))
	must(validateRebootReceipt(r, c.saved, mode))
	after := bootID(c.ctx)
	machine, e := worker(c.ctx, "cat /etc/machine-id")
	must(e)
	data, e := worker(c.ctx, "findmnt -n -o UUID /data")
	must(e)
	if after == c.saved.BootID || !validMachineIdentity(machine, c.saved.WorkerMachineID) || data != c.saved.DataUUID {
		panic("clean reboot changed worker/data identity or did not reboot")
	}
	c.report["worker_boot_after"] = after
	name := "clean_worker_reboot_resume"
	if mode == pausedLossMode {
		name = "acknowledged_pause_loss_resume"
	}
	cleanResume(c, name)
	c.report["clean_reboot_latest_data_pass"] = mode == cleanMode
	c.report["paused_acknowledged_abrupt_loss_latest_data_pass"] = mode == pausedLossMode
	c.report["abrupt_loss_tested"] = mode == pausedLossMode
	c.report["running_loss_tested"] = false
	c.report["production_stop_coordinator_tested"] = false
	c.record(string(mode) + "-data-pass")
}
func runClean() (err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("owned durability acceptance stopped; retain report and escrow for review")
		}
	}()
	if len(os.Args) != 3 || os.Geteuid() != 0 {
		return errors.New("usage: durable-worker-acceptance run|verify|cleanup[-paused-loss] PRIVATE_STAGE")
	}
	action, mode, e := parseMode(os.Args[1])
	must(e)
	stage := os.Args[2]
	if !filepath.IsAbs(stage) || filepath.Clean(stage) != stage || (!strings.HasPrefix(stage, mode.prefix()) || len(stage) <= len(mode.prefix())) {
		panic("new exact mode-specific synthetic stage required")
	}
	info, e := os.Lstat(stage)
	must(e)
	if !info.IsDir() || info.Mode().Perm() != 0700 || info.Sys().(*syscall.Stat_t).Uid != 0 {
		panic("root0700 stage required")
	}
	lock, e := os.OpenFile("/run/lock/cube-operator-acceptance.lock", os.O_RDWR|os.O_CREATE, 0600)
	must(e)
	defer lock.Close()
	must(syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB))
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	release := holdWorkerLease(ctx)
	defer release()
	env, e := os.ReadFile("/opt/baarcha-cube/worker-01/staging/cube-install.env")
	must(e)
	key := ""
	for _, line := range strings.Split(string(env), "\n") {
		if strings.HasPrefix(line, "CUBE_API_KEY=") {
			key = strings.Trim(strings.TrimPrefix(line, "CUBE_API_KEY="), "\"'")
		}
	}
	if key == "" {
		panic("Cube credential unavailable")
	}
	api, e := cube.New(cube.Config{APIURL: "http://127.0.0.1:20300", APIKey: key})
	must(e)
	c := &coordinator{ctx: ctx, stage: stage, cube: api, report: map[string]any{"production_accepted": false, "power_operation_performed_by_fixture": false, "acceptance_mode": string(mode), "template": template}}
	defer c.detach()
	if action == "run" {
		for _, name := range []string{"report.json", "intent.private.json", "escrow.private.json", "clean-reboot-complete.json", "paused-loss-complete.json"} {
			if _, e = os.Lstat(filepath.Join(stage, name)); !errors.Is(e, os.ErrNotExist) {
				panic("prior attempt exists")
			}
		}
		defer c.recordFailure()
		start := time.Now()
		c.prepare()
		c.report["prepare_through_latest_commit_milliseconds"] = time.Since(start).Milliseconds()
		if mode == cleanMode {
			cleanPause(c, "first_pause")
			cleanResume(c, "ordinary_resume")
			cleanPause(c, "pre_reboot_pause")
		} else {
			// First pause since the latest commit: an earlier successful snapshot
			// of the same marker must not mask a final-pause durability defect.
			cleanPause(c, "acknowledged_pause")
		}
		c.report["clean_reboot_ready"] = mode == cleanMode
		c.report["acknowledged_pause_loss_ready"] = mode == pausedLossMode
		c.record(mode.checkpoint())
		fmt.Println(mode.checkpoint() + ": exact owned guest acknowledged paused; root separately performs the mode-specific reviewed power action")
		deadline := time.Now().Add(15 * time.Minute)
		for {
			if _, e = os.Lstat(filepath.Join(stage, mode.receipt())); e == nil {
				break
			}
			if time.Now().After(deadline) || ctx.Err() != nil {
				panic("mode-specific reboot checkpoint expired")
			}
			time.Sleep(time.Second)
		}
		releaseAfter := holdWorkerLease(ctx)
		defer releaseAfter()
	} else {
		must(privateJSON(filepath.Join(stage, "escrow.private.json"), &c.saved))
		must(privateJSON(filepath.Join(stage, "report.json"), &c.report))
		if c.report["acceptance_mode"] != string(mode) {
			panic("saved report belongs to another durability mode")
		}
		defer c.recordFailure()
	}
	if action != "cleanup" {
		verifyDurability(c, mode)
	}
	if !c.cleanup() {
		panic("owned deletion not proven")
	}
	c.report["cleanup_verified_http404"] = true
	inventory, e := worker(ctx, "cubemastercli -a 127.0.0.1 list --all --wide")
	must(e)
	if !emptyInventory(inventory) {
		panic("postfixture inventory not zero")
	}
	c.report["provider_inventory_zero"] = true
	c.record("finished-" + string(mode))
	if action == "cleanup" {
		fmt.Println("CLEANUP: exact owned target deleted; no new durability result")
	} else {
		fmt.Println("PASS: " + string(mode) + " retained original latest app/home/SQL; owned target deleted")
	}
	return nil
}
func main() {
	if e := runClean(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
