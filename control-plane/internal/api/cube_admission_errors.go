package api

import (
	"errors"
	"net/http"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
)

// Exhausted capacity is temporary and has not started a provider mutation.
// An ambiguous allocation needs reconciliation; do not advise creating again.
func writeCubeAdmissionError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, cube.ErrCapacityUnavailable):
		w.Header().Set("Retry-After", "5")
		writeV1Err(w, http.StatusServiceUnavailable, "runtime_capacity", "Sandbox capacity is busy. Please retry shortly.")
	case errors.Is(err, cube.ErrAdmissionPending):
		writeV1Err(w, http.StatusConflict, "runtime_reconciliation_required", "The sandbox operation is awaiting reconciliation. Its saved data is retained.")
	case errors.Is(err, cube.ErrAdmissionUnknown):
		writeV1Err(w, http.StatusServiceUnavailable, "runtime_reconciliation_required", "The sandbox allocation must be reconciled before it can start.")
	default:
		return false
	}
	return true
}
