package runtime

import "errors"

const MotionWorkerCapability = "motion-worker-v1"
const MotionWorkerURL = "http://127.0.0.1:3032/__cube/motion"

// MotionStudioEnvironment changes only the target's applied environment. The
// encrypted control-plane values remain untouched for Docker rollback.
func MotionStudioEnvironment(env map[string]string, status *Status) (map[string]string, error) {
	supported := false
	if status != nil {
		for _, value := range status.Capabilities {
			if value == MotionWorkerCapability {
				supported = true
			}
		}
	}
	if !supported {
		return nil, errors.New("target template lacks Motion Studio reverse worker capability")
	}
	if env["STUDIO_WORKER_URL"] == "" || env["STUDIO_WORKER_KEY"] == "" || env["APP_ORIGIN"] == "" {
		return nil, errors.New("Motion Studio runtime configuration is incomplete")
	}
	result := make(map[string]string, len(env))
	for k, v := range env {
		result[k] = v
	}
	result["STUDIO_WORKER_URL"] = MotionWorkerURL
	return result, nil
}

// ValidateMotionStudioScope refuses a recognized worker configuration without
// the exact operator-selected application capability. It never infers authority
// from tenant configuration; that can only turn admission into a refusal.
func ValidateMotionStudioScope(env map[string]string, scoped bool) error {
	if !scoped && (env["STUDIO_WORKER_URL"] != "" || env["STUDIO_WORKER_KEY"] != "") {
		return errors.New("Motion Studio worker configuration requires an exact application capability")
	}
	return nil
}
