#!/usr/bin/env python3
"""Opt-in Traefik alias HTTP/upgrade fixture; no production or guest credentials.

Run on the VPS with its already installed Node base and Traefik image digests.
Both containers share an isolated network-none namespace and publish no ports.
This verifies edge rewriting, not the Cube runtime or deployed HTTPS chain.
"""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import sqlite3
import subprocess
import tempfile
import time
import uuid

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location("alias", ROOT / "render-preview-alias.py")
alias = importlib.util.module_from_spec(spec)
spec.loader.exec_module(alias)

BACKEND = r"""
const http=require('http'), crypto=require('crypto');
const canonical='s-01arz3ndektsv4rrffq69g5fav-3000.preview.example.test';
const server=http.createServer((req,res)=>{
 if(req.headers.host!==canonical){res.writeHead(421).end();return;}
 let body='';req.on('data',c=>body+=c);req.on('end',()=>{
  res.setHeader('content-type','application/json');res.end(JSON.stringify({method:req.method,path:req.url,body,cookie:req.headers.cookie}));
 });
});
server.on('upgrade',(req,socket)=>{
 if(req.headers.host!==canonical || req.url!='/ws?q=retained'){socket.destroy();return;}
 const key=crypto.createHash('sha1').update(req.headers['sec-websocket-key']+'258EAFA5-E914-47DA-95CA-C5AB0DC85B11').digest('base64');
 socket.write('HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: '+key+'\r\n\r\n');
 socket.write(Buffer.concat([Buffer.from([0x81,8]),Buffer.from('alias-ok')]));
});
server.listen(18081,'127.0.0.1');
"""
CLIENT = r"""
const http=require('http'),assert=require('assert');
function request(host,method='GET',path='/',body=''){
 return new Promise((resolve,reject)=>{
  const req=http.request({host:'127.0.0.1',port:18080,method,path,headers:{Host:host,Cookie:'fixture_session=synthetic','Content-Length':Buffer.byteLength(body),'X-Forwarded-Host':'s-other-3031.preview.example.test'}},res=>{
   let data='';res.on('data',c=>data+=c);res.on('end',()=>resolve({status:res.statusCode,body:data}));
  });req.on('error',reject);req.setTimeout(5000,()=>req.destroy(Error('timeout')));req.end(body);
 });
}
(async()=>{
 const r=await request('shop.preview.example.test','POST','/api/example?keep=a%2Fb&token=nonsecret-alias-capability-fixture','fixture-body');
 assert.equal(r.status,200);assert.deepEqual(JSON.parse(r.body),{method:'POST',path:'/api/example?keep=a%2Fb&token=nonsecret-alias-capability-fixture',body:'fixture-body',cookie:'fixture_session=synthetic'});
 assert.equal((await request('other.preview.example.test')).status,404);
 await new Promise((resolve,reject)=>{
  const req=http.request({host:'127.0.0.1',port:18080,path:'/ws?q=retained',headers:{Host:'shop.preview.example.test',Connection:'Upgrade',Upgrade:'websocket','Sec-WebSocket-Key':'Zml4dHVyZWZpeHR1cmUxMg==','Sec-WebSocket-Version':'13'}});
  const timer=setTimeout(()=>{req.destroy();reject(Error('upgrade timeout'));},5000);
  req.on('error',reject);req.on('upgrade',(res,socket,head)=>{
   assert.equal(res.statusCode,101);let got=head;
   function check(){if(got.length>=10){clearTimeout(timer);assert.equal(got.subarray(2,10).toString(),'alias-ok');socket.destroy();resolve();}}
   socket.on('data',chunk=>{got=Buffer.concat([got,chunk]);check()});check();
  });req.end();
 });
 console.log(JSON.stringify({http_method_path_query_body_cookie:true,unknown_host_denied:true,websocket_upgrade:true}));
})().catch(e=>{console.error(e.message);process.exit(1)});
"""


def run(*args, **kwargs):
    return subprocess.run(args, check=True, text=True, capture_output=True, timeout=40, **kwargs).stdout.strip()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--node-image", required=True)
    parser.add_argument("--traefik-image", required=True)
    args = parser.parse_args()
    if os.environ.get("CUBE_ALIAS_PROXY_TEST") != "1":
        raise SystemExit("explicit CUBE_ALIAS_PROXY_TEST=1 required")
    # Require immutable local image identities; do not pull mutable tags.
    for image in (args.node_image, args.traefik_image):
        if not image.startswith("sha256:") or len(image) != 71:
            raise SystemExit("use local immutable sha256 image IDs")
    name = "cube-alias-fixture-" + uuid.uuid4().hex[:12]
    created = []
    with tempfile.TemporaryDirectory(prefix="cube-alias-proxy-") as directory:
        os.chmod(directory, 0o755)
        db = sqlite3.connect(":memory:")
        db.executescript("CREATE TABLE sandbox(id TEXT,visibility TEXT,web_port INTEGER);CREATE TABLE sandbox_port(sandbox_id TEXT,port INTEGER);")
        sid = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
        db.execute("INSERT INTO sandbox VALUES(?,'public',3000)", (sid,))
        db.execute("INSERT INTO sandbox_port VALUES(?,3000)", (sid,))
        config = alias.render(db, sid, "shop.preview.example.test", "example.test")
        config["http"]["services"] = {"sandbox-wake": {"loadBalancer": {"servers": [{"url": "http://127.0.0.1:18081"}]}}}
        config["http"]["middlewares"]["sandbox-preview-embed"] = {"headers": {"customResponseHeaders": {"X-Frame-Options": ""}}}
        Path(directory, "dynamic.yml").write_text(json.dumps(config))
        try:
            run("docker", "run", "-d", "--name", name, "--network", "none", "--memory", "96m", "--cpus", "0.25", "--entrypoint", "node", args.node_image, "-e", BACKEND)
            created.append(name)
            run("docker", "run", "-d", "--name", name + "-proxy", "--network", "container:" + name, "--memory", "128m", "--cpus", "0.25", "--mount", f"type=bind,src={directory},dst=/fixture,readonly", args.traefik_image, "--entrypoints.web.address=:18080", "--providers.file.filename=/fixture/dynamic.yml", "--global.sendanonymoususage=false", "--global.checknewversion=false", "--accesslog.filepath=/tmp/fixture-access.log", "--accesslog.format=json", "--accesslog.fields.names.RequestPath=drop", "--accesslog.fields.headers.defaultmode=drop")
            created.append(name + "-proxy")
            for attempt in range(30):
                try:
                    result = run("docker", "exec", name, "node", "-e", CLIENT)
                    access_log = run("docker", "exec", name + "-proxy", "cat", "/tmp/fixture-access.log")
                    if "nonsecret-alias-capability-fixture" in access_log or "fixture_session=synthetic" in access_log:
                        raise AssertionError("capability or app cookie retained in access logs")
                    rows = [json.loads(line) for line in access_log.splitlines()]
                    if not rows or not all(row.get("RequestHost") and "RequestPath" not in row for row in rows):
                        raise AssertionError("host logging or path redaction missing")
                    print(json.dumps({"result": json.loads(result), "access_log_capabilities_redacted": True, "traefik_image": args.traefik_image, "node_image": args.node_image, "production_accepted": False}))
                    break
                except subprocess.CalledProcessError:
                    if attempt == 29:
                        raise
                    time.sleep(0.2)
        finally:
            for container in reversed(created):
                run("docker", "rm", "-f", container)
            db.close()


if __name__ == "__main__":
    main()
