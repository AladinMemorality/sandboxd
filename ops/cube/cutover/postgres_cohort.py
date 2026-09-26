#!/usr/bin/env python3
"""Dedicated migration of the reviewed MyHomeTroc embedded PostgreSQL project."""
import importlib.util
from pathlib import Path


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec); spec.loader.exec_module(module)
    return module


c = load('postgres_base_cohort', Path(__file__).with_name('cohort.py'))
pg = load('postgres_source', Path(__file__).with_name('postgres_source.py'))
FILES = (*c.FILES, Path(__file__).resolve(), Path(__file__).with_name('postgres_source.py').resolve())
KIND = 'current-generation-embedded-postgres-cohort'


def validate_plan(plan):
    c.m.validate_plan(plan, kind=KIND, files=FILES)


def validate_config(config):
    c.validate_config(config)
    c.need(config['version'] == 2 and config['parallelism'] == 1 and len(config['projects']) == 1,
           'Embedded PostgreSQL requires its own one-project cohort')
    row = config['projects'][0]
    c.need(row['app_id'] == pg.APP and row['sandbox_id'] == pg.SANDBOX and row['preset'] == 'react-pro'
           and 'retry_from_runtime_id' not in row, 'Exact reviewed PostgreSQL project required')


class Host(c.Host):
    def __init__(self, *args):
        super().__init__(*args)
        validate_config(self.c)
        self.pg_before = None
        self.pg_proof = None

    def validate_plan(self): validate_plan(self.plan)

    def inventory(self, name):
        if name != 'online-preflight':
            # No socket exception during planning, export or target verification.
            result = super().inventory(name)
            proof = pg.stopped(c.WORKSPACES / pg.SANDBOX, self.pg_before)
            c.need(proof == self.pg_proof, 'Frozen PostgreSQL source changed')
            return result
        report = self.run_cli(['fleet-preflight'], name, 600)
        c.need(report['identity_sha256'] == self.c['fleet_sha256'], 'Canonical fleet identity changed')
        value = next(v for v in report['projects'] if v['sandbox_id'] == pg.SANDBOX)
        row = self.c['projects'][0]
        c.need(value['target_preset'] == row['preset'] and value['template_id'] == row['template_id'], 'PostgreSQL target changed')
        if value['state'] == 'preflight_passed': return
        c.need(value['state'] == 'blocked' and value.get('reasons') == ['owner home manifest validation failed']
               and value['inventory']['home_report'].get('reasons') == ['preserved home contains special file'],
               'PostgreSQL source has another eligibility failure')
        manifest = c.b.strict(c.b.trusted(self.c['homes']))[pg.SANDBOX]
        proof = pg.live_socket_exception(c.WORKSPACES / pg.SANDBOX, manifest)
        self.event('postgres-live-socket-awaits-quiescence', proof)

    def before_controller_stop(self):
        row = self.c['projects'][0]
        self.quiet_tasks(); self.source_fence(allow_running=True)
        self.pg_before = pg.capture(c.WORKSPACES / pg.SANDBOX, self.inspect(row['container_id'])['State']['Running'])
        super().before_controller_stop()
        self.source_fence()
        self.pg_proof = pg.stopped(c.WORKSPACES / pg.SANDBOX, self.pg_before)
        self.event('postgres-source-cleanly-stopped', self.pg_proof)


if __name__ == '__main__': c.m.run_cli(Host, c.Sequence, validate_plan, __doc__)
