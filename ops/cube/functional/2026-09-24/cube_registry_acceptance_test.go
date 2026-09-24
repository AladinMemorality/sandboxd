package api

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/egress"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/idlock"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

// An opt-in disposable nested-cluster fixture, deliberately outside CI. It uses
// the real public HTTP broker without fixed-service credentials, DNS overrides,
// direct guest egress, policy exceptions, or protected-destination probes.
func TestOperatorCubeRegistryClients(t *testing.T) {
	if os.Getenv("CUBE_REGISTRY_FUNCTIONAL") != "1" {
		t.Skip("explicit disposable public-registry fixture only")
	}
	stage := os.Getenv("CUBE_REGISTRY_STAGE")
	if !filepath.IsAbs(stage) {
		t.Fatal("absolute private evidence directory required")
	}
	if _, err := os.Stat(filepath.Join(stage, "disposable-registry")); err != nil {
		t.Fatal("missing disposable stage marker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	var secret struct {
		CubeKey string `json:"cube_key"`
	}
	data, err := os.ReadFile("/root/cube-pilot/test-secrets.json")
	if err != nil || json.Unmarshal(data, &secret) != nil || secret.CubeKey == "" {
		t.Fatal("nested fixture credentials unavailable")
	}
	s, _ := newConfigTestServer(t)
	s.Locks = idlock.New()
	s.Cube, err = cube.New(cube.Config{APIURL: "http://127.0.0.1:3000", APIKey: secret.CubeKey})
	if err != nil {
		t.Fatal("Cube fixture unavailable")
	}
	project := newULID()
	s.CubeProxyURL, s.CubeDomain = "http://127.0.0.1:80", "cube.app"
	s.CubeAgentRelayOrigin, s.AgentProxyURL = "https://functional.invalid", "http://127.0.0.1:1"
	s.CubeTemplates = map[string]string{"react-pro": "tpl-ce9efc43b71248d9a0adfb90"}
	s.CubeApps = map[string]bool{project: true}
	if err = s.Store.CreateApp(ctx, &store.App{ID: project, OwnerToken: cfgTenant, Name: "Disposable public registry fixture"}); err != nil {
		t.Fatal(err)
	}
	report := map[string]any{"production_accepted": false, "network_isolation_accepted": false, "template": s.CubeTemplates["react-pro"], "direct_nic_egress_enabled": false}
	t.Cleanup(func() {
		current, e := s.Store.CurrentSandboxForApp(context.Background(), project)
		deleted := errors.Is(e, store.ErrNotFound)
		if e != nil && !deleted {
			t.Errorf("cleanup lookup: %v", e)
		}
		if e == nil {
			s.stopCubeEgress(current.ID)
			r := cubeRequest(s, "DELETE", "/v1/sandboxes/"+current.ID, "", cfgTenant)
			deleted = r.Code == 204
			if !deleted {
				t.Errorf("cleanup HTTP %d", r.Code)
			}
		}
		report["vm_deleted"] = deleted
		encoded, e := json.MarshalIndent(report, "", "  ")
		if e == nil {
			e = os.WriteFile(filepath.Join(stage, "registry-report.json"), encoded, 0600)
		}
		if e != nil {
			t.Errorf("report: %v", e)
		}
	})
	policy, err := egress.OperatorPolicy("65.108.225.153/32", "baarcha.tn,cube.app,functional.invalid", "http://127.0.0.1:3000", "http://127.0.0.1:80")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ConfigureCubeEgress(ctx, CubeEgressConfig{Policy: policy, BridgeURL: "https://functional.invalid/api/bridge"}); err != nil {
		t.Fatal(err)
	}
	r := cubeRequest(s, "POST", "/v1/apps/"+project+"/sandbox", `{"runtime_preset":"react-pro"}`, cfgTenant)
	if r.Code != 201 {
		t.Fatalf("create HTTP %d", r.Code)
	}
	var sb sandboxResp
	if json.Unmarshal(r.Body.Bytes(), &sb) != nil || sb.ID == "" {
		t.Fatal("missing sandbox")
	}
	client := s.runtimeClientFor(sb.ID)
	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	for name, content := range map[string]string{
		"sandbox.yaml": "version: 1\nweb:\n  command: node registry.mjs\n  port: 3000\nbuild:\n  command: ''\n",
		"registry.mjs": registryClientScript,
	} {
		w, e := zw.Create(name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = w.Write([]byte(content)); e != nil {
			t.Fatal(e)
		}
	}
	if err = zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err = client.ImportSource(ctx, archive.Bytes()); err != nil {
		t.Fatal(err)
	}
	for ctx.Err() == nil {
		data, e := client.ReadFile(ctx, "registry-results.json")
		if e == nil {
			var result struct {
				Node   string `json:"node"`
				Checks []struct {
					Name   string `json:"name"`
					Passed bool   `json:"passed"`
					Error  string `json:"error,omitempty"`
				} `json:"checks"`
				Complete bool `json:"complete"`
			}
			if json.Unmarshal(data, &result) != nil {
				t.Fatal("invalid guest result")
			}
			report["guest"] = result
			if result.Complete {
				if len(result.Checks) != 7 {
					t.Errorf("expected seven client checks, got %d", len(result.Checks))
				}
				for _, check := range result.Checks {
					if !check.Passed {
						t.Errorf("%s: %s", check.Name, check.Error)
					}
				}
				return
			}
		}
		time.Sleep(time.Second)
	}
	t.Fatal("registry fixture deadline")
}

const registryClientScript = `import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import https from 'node:https';
import http from 'node:http';
import {spawnSync} from 'node:child_process';
http.createServer((req,res)=>res.end('registry fixture')).listen(3000,'0.0.0.0');
const report={node:process.version,checks:[],complete:false};
const save=()=>{fs.writeFileSync('registry-results.pending',JSON.stringify(report));fs.renameSync('registry-results.pending','registry-results.json')};
const check=async(name,fn)=>{try{await fn();report.checks.push({name,passed:true})}catch(e){report.checks.push({name,passed:false,error:String(e.message).slice(0,600)})}save()};
const fresh=(label)=>fs.mkdtempSync(path.join(os.tmpdir(),'cube-registry-'+label+'-'));
const run=(command,args,cwd)=>{const r=spawnSync(command,args,{cwd,env:process.env,timeout:65000,maxBuffer:131072,encoding:'utf8'});if(r.error)throw Error(r.error.code||'spawn failed');if(r.status!==0){const known=['No module named pip','externally-managed-environment','CERTIFICATE_VERIFY_FAILED','ProxyError','ReadTimeoutError','Could not find a version','No space left on device','Permission denied'];const cause=known.find(s=>(r.stderr||'').includes(s));throw Error(command+' exit '+r.status+(cause?' ('+cause+')':''))}return r.stdout};
await check('proxy_environment',async()=>{for(const k of ['HTTP_PROXY','HTTPS_PROXY','http_proxy','https_proxy'])if(process.env[k]!=='http://127.0.0.1:3032')throw Error('incorrect '+k);if(process.env.NODE_USE_ENV_PROXY!=='1')throw Error('native Node proxy not enabled')});
await check('native_fetch_https',async()=>{const r=await fetch('https://registry.npmjs.org/-/ping',{signal:AbortSignal.timeout(20000)});if(!r.ok)throw Error('HTTP '+r.status);await r.json()});
await check('native_https_agent',()=>new Promise((resolve,reject)=>{const q=https.get('https://registry.npmjs.org/-/ping',r=>{r.resume();r.on('end',()=>r.statusCode===200?resolve():reject(Error('HTTP '+r.statusCode)))});q.setTimeout(20000,()=>q.destroy(Error('timeout')));q.on('error',reject)}));
await check('curl_pypi_https',async()=>{const text=run('curl',['--fail','--silent','--show-error','--max-time','25','https://pypi.org/pypi/packaging/24.2/json'],fresh('curl'));if(JSON.parse(text).info.version!=='24.2')throw Error('unexpected package')});
await check('npm_fresh_install',async()=>{const cwd=fresh('npm');fs.writeFileSync(path.join(cwd,'package.json'),'{}');run('npm',['install','--ignore-scripts','--no-audit','--no-fund','--fetch-retries=0','--fetch-timeout=25000','--cache',path.join(cwd,'cache'),'--registry=https://registry.npmjs.org','is-number@7.0.0'],cwd);if(JSON.parse(fs.readFileSync(path.join(cwd,'node_modules/is-number/package.json'))).version!=='7.0.0')throw Error('package mismatch')});
await check('pnpm_fresh_install',async()=>{const cwd=fresh('pnpm');fs.writeFileSync(path.join(cwd,'package.json'),'{}');run('pnpm',['add','--ignore-scripts','--store-dir',path.join(cwd,'store'),'--fetch-retries=0','--fetch-timeout=25000','--registry=https://registry.npmjs.org','is-number@7.0.0'],cwd);if(JSON.parse(fs.readFileSync(path.join(cwd,'node_modules/is-number/package.json'))).version!=='7.0.0')throw Error('package mismatch')});
await check('pip_fresh_install',async()=>{const cwd=fresh('pip');run('python3',['-m','venv',path.join(cwd,'venv')],cwd);run(path.join(cwd,'venv','bin','python'),['-m','pip','install','--disable-pip-version-check','--no-input','--no-cache-dir','--no-deps','--retries','0','--timeout','25','--index-url','https://pypi.org/simple','--target',path.join(cwd,'packages'),'packaging==24.2'],cwd);if(!fs.existsSync(path.join(cwd,'packages','packaging','__init__.py')))throw Error('package absent')});
report.complete=true;save();
`
