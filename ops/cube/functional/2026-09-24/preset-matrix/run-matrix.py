import json
import pathlib
import shlex
import subprocess

stage = pathlib.Path('/opt/baarcha-bench/cube-global-20260924/postgres-v4')
ssh = ['ssh', '-i', '/opt/baarcha-bench/cube-20260917/vm-key', '-p', '19222', '-o', 'BatchMode=yes', 'root@127.0.0.1']
rows = json.loads((stage / 'templates.json').read_text()) + json.loads((stage / 'remaining-templates.json').read_text())
assert 2 <= len(rows) <= 8 and len({r['preset'] for r in rows}) == len(rows)
result_path = stage / 'remaining-functional.json'
results = json.loads(result_path.read_text()) if result_path.exists() else []
for row in rows:
    if row['preset'] == 'node-postgres':
        continue  # Separate API/SQL/remix/restore fixture covers this preset.
    prior = [r for r in results if r['preset'] == row['preset']]
    if prior:
        assert len(prior) == 1 and prior[0]['template'] == row['template'] and prior[0]['image_digest'] == row['image_digest'] and prior[0]['functional']['delete_verified_http404'] is True
        continue
    report = '/root/cube-v4-preset-matrix/' + row['preset'] + '.json'
    command = ['/root/cube-v4-preset-matrix/reviewed-pilot', 'preset', row['template'], row['preset'], report]
    with (stage / (row['preset'] + '-functional.txt')).open('wb') as output:
        subprocess.run(ssh + [shlex.join(command)], stdout=output, stderr=output, check=True, timeout=540)
    observed = json.loads(subprocess.check_output(ssh + [shlex.join(['cat', report])], text=True))
    assert observed['delete_verified_http404'] is True
    results.append({**row, 'functional': observed})
    (stage / 'remaining-functional.json').write_text(json.dumps(results, indent=2))
    print('PASS ' + row['preset'] + ': readiness, same-process resume, strict exports, deleted HTTP404', flush=True)
