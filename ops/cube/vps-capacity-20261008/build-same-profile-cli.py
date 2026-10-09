"""Build the reviewed operator-only same-profile replacement CLI on the VPS."""
import hashlib,json,os,pathlib,re,subprocess,sys,time
P=pathlib.Path;os.umask(0o077);root=P('/opt/baarcha/operations/vps-50-profiles-20261008')
revision=sys.argv[1];assert re.fullmatch('[0-9a-f]{7,40}',revision)
release=root/'same-profile-replacement-release-20261009';release.mkdir(mode=0o700)
source=release/'source';source.mkdir(mode=0o700)
archive=root/('source-'+revision+'.tar.gz');assert archive.is_file() and not archive.is_symlink()
subprocess.run(['tar','-xzf',str(archive),'-C',str(source)],check=True,timeout=60)
with (release/'build.PRIVATE.log').open('wb') as log:
 subprocess.run(['docker','run','--rm','-v',str(source)+':/src','-v',str(release)+':/out','-v','baarcha-go122-cache:/root/.cache/go-build','-v','baarcha-go122-modules:/go/pkg/mod','-w','/src/control-plane','-e','CGO_ENABLED=1','golang:1.22-bookworm','go','build','-trimpath','-o','/out/cube-relocate','./cmd/cube-relocate'],check=True,stdout=log,stderr=subprocess.STDOUT,timeout=1800)
with (release/'cube-relocate').open('rb') as f:digest=hashlib.file_digest(f,'sha256').hexdigest()
result={'built':True,'revision':revision,'sha256':digest,'operator_cli_only':True,'model_calls':False,'b200_contacted':False,'at':time.time()}
(release/'built.json').write_text(json.dumps(result));print(json.dumps(result),flush=True)
