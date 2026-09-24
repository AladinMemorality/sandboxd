package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrivateHomeReviewedLinksRoundtripAndChangedTarget(t *testing.T) {
	root, m := homeFixture(t)
	m.Version = 2
	m.Entries = append(m.Entries, HomeManifestEntry{Path: "hubenv", Disposition: "preserve"}, HomeManifestEntry{Path: "chromelibs", Disposition: "preserve"})
	m.Links = []HomeLinkContract{
		{Path: "hubenv/bin/python3", Target: "/usr/bin/python3.13", Kind: "python-interpreter"},
		{Path: "chromelibs/usr/lib/systemd/system/hwclock.service", Target: "/dev/null", Kind: "system-package-link"},
	}
	for _, link := range m.Links {
		name := filepath.Join(root, link.Path)
		if e := os.MkdirAll(filepath.Dir(name), 0755); e != nil {
			t.Fatal(e)
		}
		if e := os.Symlink(link.Target, name); e != nil {
			t.Fatal(e)
		}
	}
	f := homeZip(t, root, m)
	st, _ := f.Stat()
	dest, _ := homeFixture(t)
	for i := 0; i < 2; i++ {
		if e := InstallPrivateHome(context.Background(), dest, m, f, st.Size()); e != nil {
			t.Fatal(e)
		}
	}
	for _, link := range m.Links {
		if got, e := os.Readlink(filepath.Join(dest, link.Path)); e != nil || got != link.Target {
			t.Fatal("literal link not restored", got, e)
		}
	}
	want, e := PrivateHomeDigest(m, f, st.Size())
	if e != nil {
		t.Fatal(e)
	}
	g := homeZip(t, dest, m)
	gs, _ := g.Stat()
	if got, e := PrivateHomeDigest(m, g, gs.Size()); e != nil || got != want {
		t.Fatal("roundtrip digest differs", e)
	}
	name := filepath.Join(root, m.Links[0].Path)
	os.Remove(name)
	os.Symlink("/etc/shadow", name)
	if _, e := ValidateHomeManifest(context.Background(), root, m); e == nil {
		t.Fatal("changed destination accepted")
	}
	// The same bytes cannot gain authorization under another owner's contract.
	m.Links[0].Target = "/usr/bin/python3"
	if _, e := PrivateHomeDigest(m, f, st.Size()); e == nil {
		t.Fatal("archive accepted with changed contract")
	}
}

func TestPrivateHomeLinkContractBoundaries(t *testing.T) {
	_, base := homeFixture(t)
	base.Version = 2
	base.Entries = append(base.Entries, HomeManifestEntry{Path: "hubenv", Disposition: "preserve"}, HomeManifestEntry{Path: "chromelibs", Disposition: "preserve"})
	valid := HomeLinkContract{Path: "hubenv/bin/python", Target: "/usr/bin/python3.13", Kind: "python-interpreter"}
	for _, link := range []HomeLinkContract{
		{Path: "hubenv/bin/python", Target: "/etc/shadow", Kind: "python-interpreter"},
		{Path: "hubenv/bin/python", Target: "/usr/bin/../bin/python3", Kind: "python-interpreter"},
		{Path: ".runtimed/bin/python", Target: "/usr/bin/python3", Kind: "python-interpreter"},
		{Path: ".claude/bin/python", Target: "/usr/bin/python3", Kind: "python-interpreter"},
		{Path: "chromelibs/usr/share/X11/rgb.txt", Target: "/etc/passwd", Kind: "system-package-link"},
		{Path: "chromelibs/etc/fonts/conf.d/../50-user.conf", Target: "/usr/share/fontconfig/conf.avail/50-user.conf", Kind: "system-package-link"},
	} {
		m := base
		m.Links = []HomeLinkContract{link}
		if _, e := CanonicalHomeManifest(m); e == nil {
			t.Fatal("unsafe contract accepted", link)
		}
	}
	m := base
	m.Version = 1
	m.Links = []HomeLinkContract{valid}
	if _, e := CanonicalHomeManifest(m); e == nil {
		t.Fatal("legacy contract accepted")
	}
	m = base
	m.Links = []HomeLinkContract{valid, valid}
	if _, e := CanonicalHomeManifest(m); e == nil {
		t.Fatal("duplicate contract accepted")
	}
	m = base
	m.Links = []HomeLinkContract{valid}
	if _, e := CanonicalHomeManifest(m); e != nil {
		t.Fatal(e)
	}
	index := ".local/share/pnpm/store/v10/projects/" + strings.Repeat("a", 30)
	for _, target := range []string{"../../../../../../../../tmp", "../../../../../../../../tmp/imgtool"} {
		if !reviewedHomeLink(HomeLinkContract{Path: index, Target: target, Kind: "pnpm-project-index"}) {
			t.Fatal("reviewed index rejected")
		}
	}
	for _, target := range []string{"../../../../../../../../etc", "../../../../../../../../tmp/other", "/tmp"} {
		if reviewedHomeLink(HomeLinkContract{Path: index, Target: target, Kind: "pnpm-project-index"}) {
			t.Fatal("unreviewed index accepted")
		}
	}
}

func TestPrivateHomeContractsCannotTraverseLinkAncestor(t *testing.T) {
	root, m := homeFixture(t)
	m.Version = 2
	m.Entries = append(m.Entries, HomeManifestEntry{Path: "hubenv/bin", Disposition: "preserve"})
	m.Links = []HomeLinkContract{{Path: "hubenv/bin/python3", Target: "/usr/bin/python3", Kind: "python-interpreter"}}
	outside := t.TempDir()
	os.Mkdir(filepath.Join(outside, "bin"), 0755)
	os.Symlink("/usr/bin/python3", filepath.Join(outside, "bin/python3"))
	os.Symlink(outside, filepath.Join(root, "hubenv"))
	if _, e := ValidateHomeManifest(context.Background(), root, m); e == nil {
		t.Fatal("link ancestor accepted")
	}
}

func TestPrivateHomeLiteralSystemdPathsRequireExactRegularContract(t *testing.T) {
	root, m := homeFixture(t)
	m.Version = 2
	m.Entries = append(m.Entries, HomeManifestEntry{Path: "chromelibs", Disposition: "preserve"})
	name := `chromelibs/usr/lib/systemd/system/system-systemd\x2dcryptsetup.slice`
	full := filepath.Join(root, name)
	if e := os.MkdirAll(filepath.Dir(full), 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(full, []byte("package unit"), 0644); e != nil {
		t.Fatal(e)
	}
	if _, e := ValidateHomeManifest(context.Background(), root, m); e == nil {
		t.Fatal("unreviewed literal name accepted")
	}
	m.LiteralPaths = []string{name}
	f := homeZip(t, root, m)
	st, _ := f.Stat()
	dest, _ := homeFixture(t)
	if e := InstallPrivateHome(context.Background(), dest, m, f, st.Size()); e != nil {
		t.Fatal(e)
	}
	if got, e := os.ReadFile(filepath.Join(dest, name)); e != nil || string(got) != "package unit" {
		t.Fatal("literal name changed", e)
	}
	if ValidArchivePath(name) {
		t.Fatal("public archive policy relaxed")
	}
	os.Remove(full)
	os.Symlink("/etc/shadow", full)
	if _, e := ValidateHomeManifest(context.Background(), root, m); e == nil {
		t.Fatal("literal contract accepted a link")
	}
	m.LiteralPaths = []string{`chromelibs/usr/lib/systemd/system/..\secret`}
	if _, e := CanonicalHomeManifest(m); e == nil {
		t.Fatal("arbitrary literal filename accepted")
	}
}
