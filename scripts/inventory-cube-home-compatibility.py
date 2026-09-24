#!/usr/bin/env python3
"""Read-only compatibility candidates; output is private, never publication data.

Run against a quiesced/read-only snapshot for definitive acceptance. Live scans
are provisional: concurrent modifications fail closed or require a repeat.
"""
import os,sys,stat,json,sqlite3,hashlib,struct,posixpath,datetime,collections,re
os.umask(0o077)
OUT=sys.argv[1]; os.makedirs(OUT,mode=0o700,exist_ok=True)
ROOT='/var/lib/sandboxd/workspaces'; DB='/var/lib/sandboxd/state/sandboxd.db'
SENSITIVE=['.runtimed','.ssh','.aws','.azure','.gnupg','.kube','.docker','.pki','.claude','.claude.json','.codex','.gemini','.config/gcloud','.config/opencode','.local/share/opencode','.git-credentials','.netrc','.npmrc']
LEAVES=['auth.json','.credentials.json','credentials.json','.git-credentials','.netrc','.npmrc']
STOCK={'.bashrc':'d28e0f5fb00ce9f17f21ac66ce05b4ae4e541b9459028639e3f848b50c8f3ffe','.profile':'d755f668dc89c4fa612a24ba44f56a497c6f102acebf7adba95eabb89654eade','.gitconfig':'6ee87852c22fa105f05076b886814f185fa685269d4f27a8e0c94b046e3f0328'}
TRUSTED_TOP=set(['.bashrc','.profile','.gitconfig','.bun','.cache','.config','.local','.npm','.npm-global','workspace'])
def sensitive(n):return n.startswith('.claude.json.') or any(n==p or n.startswith(p+'/') for p in SENSITIVE) or posixpath.basename(n) in LEAVES
def open_scoped(p, directory=False):
 # Open every ancestor without following any tenant-controlled symlink.
 if not p.startswith('/') or any(x in ('.','..') for x in p.split('/')):raise ValueError('unsafe path')
 fd=os.open('/',os.O_RDONLY|os.O_DIRECTORY)
 try:
  parts=[x for x in p.split('/') if x]
  for i,part in enumerate(parts):
   nxt=os.open(part,os.O_RDONLY|os.O_NOFOLLOW|os.O_NONBLOCK|(os.O_DIRECTORY if i<len(parts)-1 or directory else 0),dir_fd=fd)
   os.close(fd);fd=nxt
  return fd
 except BaseException:os.close(fd);raise

def readsmall(p,limit=1048576):
 fd=open_scoped(p)
 try:
  s=os.fstat(fd)
  if not stat.S_ISREG(s.st_mode) or s.st_size>limit: raise ValueError('not bounded regular')
  return os.read(fd,limit+1)
 finally:os.close(fd)
def validlink(n,t,home=False):
 if home and t.startswith('/home/sandbox/'):
  x=t[len('/home/sandbox/'):];return bool(x) and not x.startswith('/') and all(p not in ('','..','.') for p in x.split('/')) and '\\' not in x
 if re.fullmatch(r'(?:.*/)?\.venv/bin/python(?:3(?:\.[0-9]+)?)?',n) and re.fullmatch(r'/usr/bin/python3(?:\.[0-9]+)?',t):return True
 if not t or t.startswith('/') or '\\' in t or '\x00' in t:return False
 p=posixpath.normpath(posixpath.join(posixpath.dirname(n),t));return p!='..' and not p.startswith('../')
def candidate_link(n,t):
 kind=None
 if re.fullmatch(r'/usr/bin/python3(?:\.[0-9]{1,2})?',t) and re.fullmatch(r'python(?:3(?:\.[0-9]{1,2})?)?',posixpath.basename(n)) and posixpath.basename(posixpath.dirname(n))=='bin':kind='python-interpreter'
 if re.fullmatch(r'\.local/share/pnpm/store/v10/projects/[a-f0-9]{30,64}',n) and not t.startswith('/') and posixpath.normpath(posixpath.join('/home/sandbox',posixpath.dirname(n),t)) in ('/tmp','/tmp/imgtool'):kind='pnpm-project-index'
 if n.startswith('chromelibs/'):
  name=n[len('chromelibs/'):]
  known={'etc/ssh/ssh_config.d/20-systemd-ssh-proxy.conf':'/usr/lib/systemd/ssh_config.d/20-systemd-ssh-proxy.conf','etc/profile.d/70-systemd-shell-extra.sh':'/usr/lib/systemd/profile.d/70-systemd-shell-extra.sh','usr/lib/environment.d/99-environment.conf':'/etc/environment','usr/share/X11/rgb.txt':'/etc/X11/rgb.txt'}
  known.update({'usr/lib/systemd/system/'+x+'.service':'/dev/null' for x in ('hwclock','cryptdisks-early','x11-common','cryptdisks')})
  if re.fullmatch(r'etc/fonts/conf.d/[0-9]{2}-[a-z0-9-]+\.conf',name):known[name]='/usr/share/fontconfig/conf.avail/'+posixpath.basename(name)
  if known.get(name)==t:kind='system-package-link'
 if kind:return {'path':n,'target':t,'kind':kind}
 return None
def dump(n,o):
 with open(os.path.join(OUT,n),'w') as f:json.dump(o,f,sort_keys=True,indent=2)
ROOT=os.environ.get('CUBE_INVENTORY_WORKSPACES',ROOT);DB=os.environ.get('CUBE_INVENTORY_DATABASE',DB)
c=sqlite3.connect('file:'+DB+'?mode=ro',uri=True);c.row_factory=sqlite3.Row
apps=[dict(x) for x in c.execute("SELECT id,COALESCE(runtime_preset,'') preset FROM app ORDER BY id")]
rows=[dict(x) for x in c.execute("SELECT id,COALESCE(app_id,'') app_id,status,workspace_mnt,COALESCE(container_id,'') container_id FROM sandbox ORDER BY id")]
appmap={x['id']:x for x in apps};report=[];manifests={};assignments={};linkrows=[];natives=[]
for row in rows:
 sid=row['id'];home=os.path.join(ROOT,sid);rec={'sandbox_id':sid,'app_id':row['app_id'],'source_preset':appmap.get(row['app_id'],{}).get('preset',''),'status':row['status'],'issues':[],'stock_matches':{},'home_categories':{},'app_link_count':0,'home_link_count':0,'unsupported_app_links':0,'unsupported_home_links':0,'native_files':0,'custom_categories':[]};nodes={};protected=[];homeissues=[]
 if not re.fullmatch('[0-9A-HJKMNP-TV-Z]{26}',sid):rec['issues'].append('noncanonical_identity');report.append(rec);continue
 for p in [home,home+'/workspace',home+'/workspace/app']:
  fd=open_scoped(p,True);os.close(fd)
 count=[0]
 homefd=open_scoped(home,True);homedev=os.fstat(homefd).st_dev
 def walk(fd,rel,depth=0):
  if depth>32:raise RuntimeError('home depth limit')
  with os.scandir(fd) as entries:
   for ent in entries:
    count[0]+=1
    if count[0]>1000000:raise RuntimeError('home entry limit')
    n=rel+'/'+ent.name if rel else ent.name;s=ent.stat(follow_symlinks=False)
    if s.st_dev!=homedev:raise RuntimeError('home crossed mount device')
    typ='dir' if stat.S_ISDIR(s.st_mode) else 'file' if stat.S_ISREG(s.st_mode) else 'link' if stat.S_ISLNK(s.st_mode) else 'special';inapp=n=='workspace/app' or n.startswith('workspace/app/');rt=n=='.runtimed' or n.startswith('.runtimed/');sens=sensitive(n)
    if not inapp and not rt:
     nodes[n]={'kind':typ,'bytes':s.st_size if typ=='file' else 0};top=n.split('/')[0];cat=rec['home_categories'].setdefault(top,{'entries':0,'bytes':0});cat['entries']+=1;cat['bytes']+=s.st_size if typ=='file' else 0
     if sens:protected.append(n)
     if typ=='special' and not sens:homeissues.append('home_special_file')
    if typ=='link' and not rt and not sens:
     t=os.readlink(ent.name,dir_fd=fd);an=n[len('workspace/app/'):] if inapp else n;ok=validlink(an,t,not inapp);rec['app_link_count' if inapp else 'home_link_count']+=1
     if not ok:rec['unsupported_app_links' if inapp else 'unsupported_home_links']+=1
     linkrows.append({'sandbox_id':sid,'path':n,'target':t,'scope':'app' if inapp else 'home','accepted_by_archive':ok})
    # Read only executable/native file headers, never run tenant code or print content.
    custom=n.split('/')[0] not in TRUSTED_TOP and not sens and not rt
    if typ=='file' and not sens and not rt and (custom or n.startswith('.local/bin/')) and (s.st_mode&0o111 or '.so' in ent.name):
     try:
      child=os.open(ent.name,os.O_RDONLY|os.O_NOFOLLOW|os.O_NONBLOCK,dir_fd=fd)
      try:
       if not stat.S_ISREG(os.fstat(child).st_mode):raise RuntimeError('native type changed')
       b=os.read(child,4096)
      finally:os.close(child)
      if b.startswith(b'\x7fELF'):
       endian='<' if b[5]==1 else '>';machine=struct.unpack(endian+'H',b[18:20])[0];natives.append({'sandbox_id':sid,'path':n,'kind':'ELF','bits':64 if b[4]==2 else 32,'machine':machine});rec['native_files']+=1
      elif b.startswith(b'#!'):
       interp=b.split(b'\n',1)[0][2:].strip().split(b' ',1)[0].decode('utf-8','replace');natives.append({'sandbox_id':sid,'path':n,'kind':'script','interpreter':interp})
     except OSError:homeissues.append('native_header_unreadable')
    if typ=='dir' and not rt and not sens:
     child=os.open(ent.name,os.O_RDONLY|os.O_NOFOLLOW|os.O_DIRECTORY,dir_fd=fd)
     try:
      if os.fstat(child).st_dev!=homedev:raise RuntimeError('home crossed mount device')
      walk(child,n,depth+1)
     finally:os.close(child)
 try:walk(homefd,'')
 finally:os.close(homefd)
 rec['custom_categories']=sorted(set(rec['home_categories'])-TRUSTED_TOP-set(SENSITIVE))
 entries=[{'path':'.runtimed','disposition':'separate'},{'path':'workspace/app','disposition':'separate'}]
 def select(n):
  if n=='.runtimed' or n=='workspace/app':return
  if sensitive(n):entries.append({'path':n,'disposition':'retained','reason':'Provider or authentication state remains on private source; destination receives fresh platform identity'});return
  # Workspace must split around app; other parents split only around protected children.
  split=n=='workspace' or any(p.startswith(n+'/') for p in protected)
  if split:
   for child in sorted(nodes):
    if posixpath.dirname(child)==n:select(child)
  else:entries.append({'path':n,'disposition':'preserve'})
 for n in sorted(nodes):
  if '/' not in n:select(n)
 for n,h in STOCK.items():
  try:rec['stock_matches'][n]=hashlib.sha256(readsmall(home+'/'+n)).hexdigest()==h
  except (OSError,ValueError):rec['stock_matches'][n]=False
 contracts=[candidate_link(x['path'],x['target']) for x in linkrows if x['sandbox_id']==sid and x['scope']=='home' and not x['accepted_by_archive']]
 rec['candidate_link_contracts']=sum(x is not None for x in contracts)
 rec['unreviewed_home_links']=sum(x is None for x in contracts)
 literal_paths=[n for n in nodes if n in (r'chromelibs/usr/lib/systemd/system/system-systemd\x2dcryptsetup.slice',r'chromelibs/usr/lib/systemd/system/system-systemd\x2dveritysetup.slice') and nodes[n]['kind']=='file']
 m={'version':2 if any(contracts) or literal_paths else 1,'entries':sorted(entries,key=lambda x:x['path'])}
 if literal_paths:m['literal_paths']=sorted(literal_paths)
 if any(contracts):m['links']=sorted((x for x in contracts if x),key=lambda x:x['path'])
 manifests[sid]=m
 if len(json.dumps(m,separators=(',',':')).encode())>(32768 if m['version']==2 else 4096) or len(entries)>96:rec['issues'].append('manifest_selector_limit')
 rec['issues']+=sorted(set(homeissues))
 if rec['unsupported_home_links']:rec['issues'].append('unsupported_home_symlink')
 if rec['unsupported_app_links']:rec['issues'].append('unsupported_app_symlink')
 if rec['native_files'] or rec['custom_categories']:rec['issues'].append('custom_tool_runtime_compatibility_requires_guest_verification')
 # Only selected capability booleans and hashes leave process; package/scripts/commands never emitted.
 app=home+'/workspace/app';caps={};pkg={}
 try:
  raw=readsmall(app+'/package.json');pkg=json.loads(raw);deps={**pkg.get('dependencies',{}),**pkg.get('devDependencies',{})};caps={x:x in deps for x in ['vite','react','next','express','tailwindcss','lucide-react','@radix-ui/react-dialog']};rec['package_sha256']=hashlib.sha256(raw).hexdigest()
 except (OSError,ValueError,TypeError):rec['issues'].append('package_manifest_unavailable_or_non_json')
 rec['package_capabilities']=caps
 try:
  raw=readsmall(app+'/sandbox.yaml');text=raw.decode('utf-8');rec['sandbox_yaml_sha256']=hashlib.sha256(raw).hexdigest();rec['web_command_capabilities']={x:bool(re.search(r'(?<![\w-])'+re.escape(x)+r'(?![\w-])',text)) for x in ['vite','next','node','uvicorn','python3','pnpm','npm','bun']};rec['declared_ports']=[int(x) for x in re.findall(r'^\s*port:\s*([0-9]+)\s*$',text,re.M)]
 except (OSError,ValueError,UnicodeError):rec['issues'].append('sandbox_yaml_unavailable');rec['web_command_capabilities']={}
 if not rec['source_preset']:
  # Candidate requires agreement of dependency and web command, not filename guessing.
  cmd=rec['web_command_capabilities'];candidate=''
  if caps.get('next') and cmd.get('next'):candidate='nextjs'
  elif caps.get('vite') and caps.get('react') and cmd.get('vite'):candidate='react-pro' if caps.get('@radix-ui/react-dialog') and caps.get('tailwindcss') else 'react-vite'
  elif caps.get('express') and cmd.get('node'):candidate='node-express'
  rec['candidate_preset']=candidate
  if candidate:assignments[row['app_id']]=candidate;rec['preset_rationale']='Installed package capabilities and declared web-command family agree; no source or manifest replacement proposed'
  else:rec['issues'].append('missing_preset_requires_further_review')
 else:rec['candidate_preset']=rec['source_preset'];rec['preset_rationale']='Existing durable project preset retained'
 report.append(rec)
allreport={'observed_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'scope':'read-only metadata, stock hashes, selected package capabilities and native headers; no code executed','sandbox_count':len(rows),'app_count':len(apps),'apps_without_sandbox':[x for x in apps if x['id'] not in {r['app_id'] for r in rows}],'projects':report}
dump('review-private.json',allreport);dump('candidate-home-manifests.json',manifests);dump('candidate-preset-assignments.json',assignments);dump('links-private.json',linkrows);dump('native-headers-private.json',natives)
print(json.dumps({'sandboxes':len(rows),'apps':len(apps),'candidate_manifests':len(manifests),'preset_candidates':len(assignments),'unsupported_home_links':sum(x['unsupported_home_links'] for x in report),'unsupported_app_links':sum(x['unsupported_app_links'] for x in report),'candidate_link_contracts':sum(x['candidate_link_contracts'] for x in report),'unreviewed_home_links':sum(x['unreviewed_home_links'] for x in report),'issue_counts':dict(collections.Counter(k for x in report for k in x['issues']))}))
