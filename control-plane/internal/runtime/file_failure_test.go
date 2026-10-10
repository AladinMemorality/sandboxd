package runtime

import "testing"

func TestFileFailureReasonNeverReflectsUnknownGuestText(t *testing.T) {
	for _, body := range []string{"secret-token", "workspace is quiesced for migration secret-token", `{"error":"secret-token"}`} {
		if got := safeFileFailureReason(409, []byte(body)); got != "" {
			t.Fatalf("unknown body reflected: %q", got)
		}
	}
	if got := safeFileFailureReason(409, []byte("workspace is quiesced for migration\n")); got != "workspace_quiesced" {
		t.Fatalf("reason: %q", got)
	}
	if got := safeFileFailureReason(500, []byte("supervisor busy")); got != "" {
		t.Fatalf("wrong status: %q", got)
	}
}
