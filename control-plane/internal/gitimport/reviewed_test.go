package gitimport

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReviewedGitRejectsSSRFAndRedirectTargets(t *testing.T) {
	for _, u := range []string{"https://127.0.0.1/x", "https://github.com.evil.example/a/b", "https://github.com:444/a/b", "https://github.com/a/b?redirect=x", "https://github.com/a/%2e%2e/b", "https://user@github.com/a/b", "https://git.internal/a/b"} {
		if ValidateReviewedRepoURL(u) == nil {
			t.Fatal("accepted " + u)
		}
	}
	if e := ValidateReviewedRepoURL("https://github.com/org/repo.git"); e != nil {
		t.Fatal(e)
	}
	for _, s := range []string{"127.0.0.1", "10.1.2.3", "172.17.0.1", "169.254.169.254", "100.64.0.1", "198.18.0.1", "0.1.2.3"} {
		if publicGitIP(net.ParseIP(s)) {
			t.Fatal("accepted protected IP " + s)
		}
	}
}
func TestReviewedClonePinsAddressAndDisablesAmbientCredentials(t *testing.T) {
	_, envPath, _ := fakeGit(t)
	t.Setenv("GLOBAL_PROVIDER_KEY", "must-not-reach-git")
	if e := Clone(context.Background(), Spec{RepoURL: "https://github.com/org/repo", Branch: "main", DestDir: filepath.Join(t.TempDir(), "app"), Token: aToken, reviewedResolve: "github.com:443:140.82.114.3"}); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(envPath)
	s := string(b)
	for _, want := range []string{"http.curloptResolve", "github.com:443:140.82.114.3", "http.followRedirects", "credential.helper", "GIT_CONFIG_GLOBAL=/dev/null"} {
		if !strings.Contains(s, want) {
			t.Fatal("missing " + want)
		}
	}
	if strings.Contains(s, "GLOBAL_PROVIDER_KEY") || strings.Contains(s, aToken) {
		t.Fatal("ambient credential leaked")
	}
}
