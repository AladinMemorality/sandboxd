//go:build !cube_workload_benchmark

package cube

// BenchmarkAdmissionBuild is false in every ordinary production build.
const BenchmarkAdmissionBuild = false

// GuardedAdmissionLimit is a compiled ceiling, never a live policy override.
const GuardedAdmissionLimit = 4

func admissionProfileAllowed(cpu, memory int) bool { return cpu == 2 && memory == 2048 }
