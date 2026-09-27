#!/usr/bin/env python3
"""Populate destination-local test caches directly from S3 without starting VMs."""
import concurrent.futures, importlib.util, json, os
from pathlib import Path

def main():
    os.umask(0o077)
    spec=importlib.util.spec_from_file_location('worker',Path(__file__).with_name('import-test-copy.py'))
    worker=importlib.util.module_from_spec(spec);spec.loader.exec_module(worker)
    raw=Path('/root/prewarm-test-copies.PRIVATE.json').read_bytes()
    assert len(raw)<=524288
    jobs=json.loads(raw)
    assert len(jobs)<=100 and all(j.get('cache_only') is True for j in jobs)
    results=[]
    def run(job):
        try:return worker.main(job)
        except Exception as error:return dict(app_id=job['app_id'],error=type(error).__name__)
    with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:
        for result in pool.map(run,jobs):
            results.append(result)
            Path('/root/prewarm-test-copies-result.json').write_text(json.dumps(results))
            print(json.dumps(result),flush=True)

if __name__=='__main__':main()
