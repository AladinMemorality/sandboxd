//go:build cube_workload_benchmark

package cube

// BenchmarkAdmissionBuild identifies an explicitly tagged operator test binary.
// Production entrypoints still call RequireStorageGuard, which refuses every
// non-production profile/capacity even when this tag is accidentally supplied.
const BenchmarkAdmissionBuild = true
const GuardedAdmissionLimit = 12

func admissionProfileAllowed(cpu, memory int) bool {
	return (cpu == 1 && (memory == 1024 || memory == 2048)) || (cpu == 2 && memory == 2048)
}
