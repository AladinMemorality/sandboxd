"""One planned VPS-only cycle: 40 -> 48 GiB and qcow2 discard support.

Install the new launch contract only after the old supervisor has completed its
normal pause/clean shutdown. Never substitute a forced stop or invented receipt.
"""
import importlib.util
import os
from pathlib import Path

spec = importlib.util.spec_from_file_location('planned', '/usr/local/libexec/baarcha-cube-planned.py')
p = importlib.util.module_from_spec(spec); spec.loader.exec_module(p)
ROOT = Path('/opt/baarcha/operations/vps-50-profiles-20261008')
CANDIDATE = ROOT / 'lifecycle-48g.py'
CONTRACT = ROOT / 'resize-contract.json'

class Host(p.Host):
    def preflight(self, defer_busy=False):
        contract=p.b.strict(p.b.trusted(CONTRACT))
        p.need(set(contract)=={'old_sha256','new_sha256'}, 'exact resize contract required')
        p.need(p.b.digest(p.b.LIFECYCLE)==contract['old_sha256'] and
               p.b.digest(CANDIDATE)==contract['new_sha256'], 'resize source changed')
        memory={line.split(':')[0]:int(line.split()[1])*1024 for line in Path('/proc/meminfo').read_text().splitlines()}
        p.need(memory['MemAvailable']>=16*1024**3, 'resize needs 8 GiB plus 8 GiB host headroom')
        return super().preflight(defer_busy=defer_busy)

    def authorize_start(self, receipt):
        self.stopped_fence(receipt)
        contract=p.b.strict(p.b.trusted(CONTRACT))
        p.need(p.b.digest(p.b.LIFECYCLE)==contract['old_sha256'] and
               p.b.digest(CANDIDATE)==contract['new_sha256'], 'resize source changed after clean stop')
        before=self.job/'supervisor-before.py'
        if before.exists():p.need(p.b.digest(before)==contract['old_sha256'],'saved supervisor changed')
        else:p.x.publish(before,p.b.trusted(p.b.LIFECYCLE,False))
        # boot_transition.atomic intentionally accepts private JSON only.
        # Executables have a distinct, exact-hash atomic replacement contract.
        temporary=p.b.LIFECYCLE.with_name('.capacity-supervisor-new')
        fd=os.open(temporary,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o755)
        with os.fdopen(fd,'wb') as file:
            file.write(p.b.trusted(CANDIDATE));file.flush();os.fsync(file.fileno())
        temporary.chmod(0o755);os.replace(temporary,p.b.LIFECYCLE)
        fd=os.open(p.b.LIFECYCLE.parent,os.O_RDONLY)
        try:os.fsync(fd)
        finally:os.close(fd)
        drop=Path('/etc/systemd/system/baarcha-cube-worker-01.service.d/60-capacity.conf')
        drop.parent.mkdir(exist_ok=True)
        p.need(not drop.exists(),'resize drop-in already exists')
        p.b.atomic(drop,b'[Service]\nMemoryHigh=50G\nMemoryMax=52G\nMemorySwapMax=0\n')
        self.command(['/usr/bin/systemctl','daemon-reload'])
        self.event('resize-installed',{'memory_gib':48,'discard':'unmap','supervisor_sha256':contract['new_sha256']})
        super().authorize_start(receipt)

if __name__=='__main__':
    p.m.run_cli(Host,p.Sequence,p.validate_plan,__doc__)
