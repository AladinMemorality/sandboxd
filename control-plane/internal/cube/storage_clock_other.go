//go:build !linux

package cube

// Production observation accounting is supported only on the reviewed Linux
// host clock. Unit tests inject a clock; unsupported hosts fail closed.
func ReadStorageClock() (StorageClock, error) { return StorageClock{}, ErrStorageUnavailable }
