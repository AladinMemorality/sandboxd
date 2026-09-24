package runtime

import (
	"errors"
	"path"
	"regexp"
	"sort"
	"strings"
)

// HomeLinkContract preserves one reviewed link literally. It grants no right to
// read its target: all home traversal and installation remain descriptor-scoped.
// It is transport permission, not proof of destination interpreter/library ABI.
type HomeLinkContract struct {
	Path   string `json:"path"`
	Target string `json:"target"`
	Kind   string `json:"kind"`
}

var homePythonTarget = regexp.MustCompile(`^/usr/bin/python3(\.[0-9]{1,2})?$`)
var homePythonLeaf = regexp.MustCompile(`^python(3(\.[0-9]{1,2})?)?$`)
var homePnpmIndex = regexp.MustCompile(`^\.local/share/pnpm/store/v10/projects/[a-f0-9]{30,64}$`)
var homeFontConfig = regexp.MustCompile(`^[0-9]{2}-[a-z0-9-]+\.conf$`)

func reviewedHomeLink(link HomeLinkContract) bool {
	switch link.Kind {
	case "python-interpreter":
		return homePythonTarget.MatchString(link.Target) && homePythonLeaf.MatchString(path.Base(link.Path)) && path.Base(path.Dir(link.Path)) == "bin"
	case "pnpm-project-index":
		// These are package-manager index pointers, never archive traversal roots.
		resolved := path.Clean(path.Join("/home/sandbox", path.Dir(link.Path), link.Target))
		return homePnpmIndex.MatchString(link.Path) && !path.IsAbs(link.Target) && (resolved == "/tmp" || resolved == "/tmp/imgtool")
	case "system-package-link":
		// Only the observed unpacked package layout, never arbitrary OS paths.
		name := strings.TrimPrefix(link.Path, "chromelibs/")
		if name == link.Path {
			return false
		}
		if strings.HasPrefix(name, "etc/fonts/conf.d/") && homeFontConfig.MatchString(path.Base(name)) {
			return name == "etc/fonts/conf.d/"+path.Base(name) && link.Target == "/usr/share/fontconfig/conf.avail/"+path.Base(name)
		}
		allowed := map[string]string{
			"etc/ssh/ssh_config.d/20-systemd-ssh-proxy.conf": "/usr/lib/systemd/ssh_config.d/20-systemd-ssh-proxy.conf",
			"etc/profile.d/70-systemd-shell-extra.sh":        "/usr/lib/systemd/profile.d/70-systemd-shell-extra.sh",
			"usr/lib/environment.d/99-environment.conf":      "/etc/environment",
			"usr/share/X11/rgb.txt":                          "/etc/X11/rgb.txt",
		}
		for _, service := range []string{"hwclock", "cryptdisks-early", "x11-common", "cryptdisks"} {
			allowed["usr/lib/systemd/system/"+service+".service"] = "/dev/null"
		}
		return allowed[name] != "" && allowed[name] == link.Target
	}
	return false
}

func canonicalHomeLinks(manifest *HomeManifest) error {
	if len(manifest.LiteralPaths) > 2 || (manifest.Version == 1 && len(manifest.LiteralPaths) > 0) {
		return errors.New("unsupported literal home paths")
	}
	manifest.LiteralPaths = append([]string(nil), manifest.LiteralPaths...)
	sort.Strings(manifest.LiteralPaths)
	for i, name := range manifest.LiteralPaths {
		entry, ok := homeDisposition(name, *manifest)
		if !reviewedHomeLiteralPath(name) || !ok || entry.Disposition != "preserve" || (i > 0 && manifest.LiteralPaths[i-1] == name) {
			return errors.New("unreviewed literal home path")
		}
	}
	if len(manifest.Links) > 128 || (manifest.Version == 1 && len(manifest.Links) != 0) {
		return errors.New("unsupported home link contracts")
	}
	manifest.Links = append([]HomeLinkContract(nil), manifest.Links...)
	sort.Slice(manifest.Links, func(i, j int) bool { return manifest.Links[i].Path < manifest.Links[j].Path })
	for i, link := range manifest.Links {
		entry, ok := homeDisposition(link.Path, *manifest)
		if !ValidArchivePath(link.Path) || len(link.Path) > 1024 || len(link.Target) > 1024 || strings.ContainsRune(link.Target, 0) || sensitiveHomePath(link.Path) || !ok || entry.Disposition != "preserve" || !reviewedHomeLink(link) || (i > 0 && manifest.Links[i-1].Path == link.Path) {
			return errors.New("unreviewed home link contract")
		}
	}
	return nil
}

func manifestHomeLink(manifest HomeManifest, name, target string) bool {
	for _, link := range manifest.Links {
		if link.Path == name {
			return link.Target == target && reviewedHomeLink(link)
		}
	}
	return privateHomeLink(name, target)
}

// Linux systemd unit escaping uses a literal backslash, not a path separator.
// Only these regular package files are eligible; public archives stay strict.
func reviewedHomeLiteralPath(name string) bool {
	return name == `chromelibs/usr/lib/systemd/system/system-systemd\x2dcryptsetup.slice` || name == `chromelibs/usr/lib/systemd/system/system-systemd\x2dveritysetup.slice`
}
func privateHomePath(manifest HomeManifest, name string) bool {
	if ValidArchivePath(name) {
		return true
	}
	if manifest.Version != 2 || !reviewedHomeLiteralPath(name) {
		return false
	}
	for _, literal := range manifest.LiteralPaths {
		if literal == name {
			return true
		}
	}
	return false
}
