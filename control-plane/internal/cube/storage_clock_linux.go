//go:build linux

package cube

import (
	"golang.org/x/sys/unix"
	"os"
	"strings"
)

// CLOCK_BOOTTIME is shared with the outer root observer. Docker must not use a
// separate time namespace. Unlike wall time this includes suspend and cannot
// move backwards following NTP/operator clock correction.
func ReadStorageClock() (StorageClock, error) {
	b, e := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if e != nil {
		return StorageClock{}, ErrStorageUnavailable
	}
	var ts unix.Timespec
	if unix.ClockGettime(unix.CLOCK_BOOTTIME, &ts) != nil {
		return StorageClock{}, ErrStorageUnavailable
	}
	boot := strings.TrimSpace(string(b))
	if !storageUUID.MatchString(boot) {
		return StorageClock{}, ErrStorageUnavailable
	}
	return StorageClock{BootID: boot, NS: ts.Nano()}, nil
}
