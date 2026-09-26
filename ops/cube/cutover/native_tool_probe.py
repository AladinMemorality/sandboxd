"""Bounded offline tool probes for five reviewed owner homes.

Run inside the exact source/target guest. All generated files go to a disposable
temporary directory, never the transferred owner home. No network is requested.
"""
import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import tempfile
import time
import urllib.request

PYTHON = {
    '01M3BNSE0265HAEFSYJEYJVVR9': '.imgenv/bin/python',
    '01M2P0ME8C8NYPCAM0PNF1ADSK': '.cache/uv/archive-v0/OQNy4ffHC8y-emP0NLGo0/bin/python',
}
SHARP = '01M2E2NBZV6ZRTNNKC3JG50HJY'
CHROME = {
    '01M1RVEEX30YK93FEHN50FZXT0': ('chrom-bin/chromium', 'chrom-libs'),
    '01M1HJ4EXF1GS6GE3BS9G3ANF3': (
        '.cache/puppeteer/chrome/linux-131.0.6778.204/chrome-linux64/chrome',
        'chromelibs/usr/lib/x86_64-linux-gnu'),
}
SUPPORTED = frozenset(PYTHON) | frozenset(CHROME) | {SHARP}


def chrome_automation(command, env, directory):
    """Headless-shell builds expose CDP but may not implement --dump-dom."""
    port_file = Path(directory) / 'profile/DevToolsActivePort'
    with tempfile.TemporaryFile() as log:
        process = subprocess.Popen(command + ['--remote-debugging-port=0', 'about:blank'],
                                   env=env, cwd=directory, stdout=log, stderr=log,
                                   start_new_session=True)
        try:
            deadline = time.monotonic() + 10
            while not port_file.exists():
                if process.poll() is not None or time.monotonic() >= deadline:
                    raise RuntimeError('Chromium automation did not become ready')
                time.sleep(0.1)
            port = int(port_file.read_text().splitlines()[0])
            if not 1024 <= port <= 65535: raise RuntimeError('Invalid Chromium loopback port')
            opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
            with opener.open('http://127.0.0.1:%d/json/list' % port, timeout=3) as response:
                targets = json.loads(response.read(65536))
            target = next(t for t in targets if t.get('type') == 'page' and t.get('url') == 'about:blank')
            address = target['webSocketDebuggerUrl']
            if not address.startswith('ws://127.0.0.1:%d/devtools/page/' % port):
                raise RuntimeError('Chromium automation must remain on its own loopback listener')
            script = """const ws = new WebSocket(process.argv[1]);
const deadline = setTimeout(() => process.exit(2), 8000);
ws.onopen = () => ws.send(JSON.stringify({id: 1, method: 'Runtime.evaluate', params: {
  expression: 'document.documentElement.outerHTML', returnByValue: true}}));
ws.onerror = () => process.exit(3);
ws.onmessage = event => {
  const value = JSON.parse(event.data);
  if (value.id !== 1) return;
  const html = value.result?.result?.value;
  if (typeof html !== 'string' || !html.includes('<html') || !html.includes('<body')) process.exit(4);
  console.log(JSON.stringify({local_dom_rendered: true, automation_protocol: true}));
  clearTimeout(deadline); ws.close();
};
"""
            result = subprocess.run(['node', '-e', script, address], env=env, cwd=directory,
                                    capture_output=True, timeout=12)
            if result.returncode != 0: raise RuntimeError('Chromium local DOM automation failed')
            proof = json.loads(result.stdout)
            if proof != {'local_dom_rendered': True, 'automation_protocol': True}:
                raise RuntimeError('Invalid Chromium automation proof')
            return proof
        finally:
            # Only the new browser process group belongs to this probe.
            try: os.killpg(process.pid, signal.SIGTERM)
            except ProcessLookupError: pass
            try: process.wait(timeout=3)
            except subprocess.TimeoutExpired:
                os.killpg(process.pid, signal.SIGKILL); process.wait(timeout=3)


def probe(sandbox_id):
    if sandbox_id not in SUPPORTED: raise RuntimeError('Unreviewed native-tool project')
    if os.getuid() == 0:
        os.setgroups([]); os.setgid(1000); os.setuid(1000)
    if os.getuid() != 1000 or os.getgid() != 1000:
        raise RuntimeError('Run tool probes as the sandbox owner')
    home = Path('/home/sandbox')
    with tempfile.TemporaryDirectory(prefix='cube-tool-check-') as directory:
        env = {'HOME': str(home), 'PATH': '/usr/local/bin:/usr/bin:/bin',
               'LANG': 'C.UTF-8', 'TMPDIR': directory, 'PYTHONDONTWRITEBYTECODE': '1'}
        if sandbox_id in PYTHON:
            command = [str(home / PYTHON[sandbox_id]), '-B', '-c',
                "import io,json,sys;from PIL import Image;"
                "assert sys.version_info[:2]==(3,13);"
                "b=io.BytesIO();Image.new('RGB',(2,2),(1,2,3)).save(b,format='PNG');"
                "assert Image.open(io.BytesIO(b.getvalue())).getpixel((0,0))==(1,2,3);"
                "print(json.dumps({'python':'3.13','pillow_png':True}))"]
            kind = 'python-pillow'
        elif sandbox_id == SHARP:
            command = ['node', '--input-type=module', '-e',
                "import{createRequire}from'node:module';"
                "const require=createRequire('/home/sandbox/.imgtools/package.json');"
                "const sharp=require('sharp'),esbuild=require('esbuild');"
                "const b=await sharp({create:{width:2,height:2,channels:3,background:'#010203'}}).png().toBuffer();"
                "const m=await sharp(b).metadata();if(m.width!==2||m.height!==2)process.exit(1);"
                "const r=esbuild.transformSync('const n:number=1',{loader:'ts'});if(!r.code.includes('1'))process.exit(1);"
                "console.log(JSON.stringify({sharp_png:true,esbuild_typescript:true}));"]
            kind = 'sharp-esbuild'
        else:
            executable, libraries = CHROME[sandbox_id]
            # Chromium may create an extensions/ directory beside its executable
            # even with a disposable profile. Run an exact copy of the preserved
            # bundle so compatibility checks cannot change the imported home.
            source = home / executable
            bundle = Path(directory) / 'browser'
            shutil.copytree(source.parent, bundle, symlinks=True)
            env.update(HOME=directory, XDG_CACHE_HOME=directory + '/cache',
                       XDG_CONFIG_HOME=directory + '/config')
            env['LD_LIBRARY_PATH'] = str(home / libraries)
            command = [str(bundle / source.name), '--headless', '--no-sandbox', '--disable-gpu',
                       '--disable-dev-shm-usage', '--disable-background-networking',
                       '--no-first-run', '--no-default-browser-check', '--no-pings',
                       '--user-data-dir=' + directory + '/profile']
            kind = 'chromium-dom'
            if sandbox_id == '01M1RVEEX30YK93FEHN50FZXT0':
                proof = chrome_automation(command, env, directory)
                return {'sandbox_id': sandbox_id, 'kind': kind, 'success': True, 'proof': proof}
            command.extend(['--dump-dom', 'about:blank'])
        result = subprocess.run(command, env=env, cwd=directory, capture_output=True, timeout=30)
        if result.returncode != 0:
            # Do not include owner program output in a public acceptance event.
            raise RuntimeError('Native tool check failed: ' + kind)
        if kind == 'chromium-dom':
            if b'<html' not in result.stdout.lower() or b'<body' not in result.stdout.lower():
                raise RuntimeError('Chromium did not render the local blank document')
            proof = {'local_dom_rendered': True}
        else:
            proof = json.loads(result.stdout)
        return {'sandbox_id': sandbox_id, 'kind': kind, 'success': True, 'proof': proof}


if __name__ == '__main__':
    import sys
    if len(sys.argv) != 2: raise SystemExit('Exact reviewed sandbox ID required')
    print('CUBE_NATIVE_TOOL=' + json.dumps(probe(sys.argv[1]), sort_keys=True))
