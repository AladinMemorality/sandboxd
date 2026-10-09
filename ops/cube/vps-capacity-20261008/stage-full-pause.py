import pathlib,subprocess,hashlib,os,json
P=pathlib.Path;os.umask(0o077)
assert P('/etc/machine-id').read_text().strip()=='2b9e31d4abd345e3bd4b966591e61296'
source=P('/root/cube-production/containerd-durable-native-candidate');out=P('/root/vps-full-pause-20261009-02');out.mkdir(mode=0o700)
installed=P('/usr/local/services/cubetoolbox/Cubelet/bin/cubelet')
with installed.open('rb') as f:oldhash=hashlib.file_digest(f,'sha256').hexdigest()
assert oldhash=='de3bd4c1a4db12c11d58cf7f558589f04ab4b3d736d4e72a947d45b8343bef9b'
with (source/'cubelet-candidate').open('rb') as f:assert hashlib.file_digest(f,'sha256').hexdigest()==oldhash
with (out/'original-source-check.log').open('wb') as log:subprocess.run(['sha256sum','-c','final-source.sha256'],cwd=source,stdout=log,stderr=subprocess.STDOUT,check=True,timeout=120)
(out/'installed-sha256.txt').write_text(oldhash)
for name in ['Cubelet','CubeNet','pkgs']:
 subprocess.run(['cp','-a','--reflink=auto',str(source/name),str(out/name)],check=True,timeout=180)
p=out/'Cubelet/services/cubebox/pause_cow.go';old=p.read_text();before='SnapshotType:   normalizeSnapshotType(snapshotType),';assert old.count(before)==1
new=old.replace('func newPauseSnapshotConfig(dest, memURL, snapshotType string)', 'func newPauseSnapshotConfig(dest, memURL, _ string)').replace(before,'''// Pause must retain every page of the currently frozen VM. Incremental
        // anonymous-page classification is not sufficient evidence that omitted
        // memory equals the selected base. Repeated restores have exhibited
        // guest kernel list corruption; use the existing complete dump path.
        SnapshotType: snapshotTypeFull,''');p.write_text(new)
(out/'pause_cow.go.before').write_text(old)
t=out/'Cubelet/services/cubebox/pause_cow_test.go';oldtest=t.read_text();(out/'pause_cow_test.go.before').write_text(oldtest)
# Replace only the pause wire policy test with explicit coverage of every
# proposed incremental policy. Commit snapshot selection is unchanged.
a=oldtest.index('func TestNewPauseSnapshotConfigCarriesSnapshotType');z=oldtest.find('\nfunc ',a+1);z=len(oldtest) if z<0 else z
newtest='''func TestPauseSnapshotAlwaysCapturesFullMemory(t *testing.T) {
 for _, proposed := range []string{snapshotTypeFull, snapshotTypeIncremental, snapshotTypeSoftDirty, "", "unknown"} {
  cfg := newPauseSnapshotConfig("/snapshot", "file:///memory", proposed)
  if cfg.SnapshotType != snapshotTypeFull || cfg.DestinationURL != "/snapshot" || cfg.MemoryVolURL == nil || *cfg.MemoryVolURL != "file:///memory" { t.Fatalf("unsafe pause config for %q: %#v", proposed, cfg) }
  data, err := json.Marshal(cfg); if err != nil { t.Fatal(err) }
  var got pauseSnapshotConfig; if err := json.Unmarshal(data, &got); err != nil { t.Fatal(err) }
  if got.SnapshotType != snapshotTypeFull { t.Fatal("full snapshot policy lost on wire") }
 }
 if newPauseSnapshotConfig("/snapshot", "", snapshotTypeIncremental).MemoryVolURL != nil { t.Fatal("unexpected volume override") }
}
'''
t.write_text(oldtest[:a]+newtest+oldtest[z:]);(out/'stage.json').write_text(json.dumps({'old_binary_sha256':oldhash,'original_source_unchanged':True,'candidate_only':True}));print('isolated native source staged; installed binary unchanged')
