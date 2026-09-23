#!/usr/bin/env python3
"""Offline configuration/capacity/evidence inventory. Never enables networking."""
import argparse
import hashlib
import ipaddress
import json
import os
from pathlib import Path
import re
import stat
import sys
from urllib.parse import urlsplit

PRESETS = {'react-pro', 'marketplace', 'react-vite', 'nextjs', 'node-express', 'fastapi', 'worker'}
EVIDENCE = ('network-isolation', 'claude-model-metering', 'bridge-assets', 'dependency-registry',
            'preview-browser-tls', 'backup-restore', 'worker-recovery', 'concurrent-load', 'template-review')
CONFIG_KEYS = {
    'SANDBOXD_CUBE_ENABLED', 'SANDBOXD_CUBE_ROLLOUT', 'SANDBOXD_CUBE_APP_IDS',
    'SANDBOXD_CUBE_API_URL', 'SANDBOXD_CUBE_API_KEY', 'SANDBOXD_CUBE_PROXY_URL',
    'SANDBOXD_CUBE_DOMAIN', 'SANDBOXD_CUBE_TEMPLATES', 'SANDBOXD_CUBE_EGRESS_ALLOW_DOMAINS',
    'SANDBOXD_CUBE_AGENT_RELAY_ORIGIN', 'SANDBOXD_CUBE_AGENT_RELAY_NETWORK_VERIFIED',
    'SANDBOXD_AGENT_PROXY_URL', 'SANDBOXD_PREVIEW_TOKEN_SECRETS', 'SANDBOXD_API_AUTH_DISABLED',
    'SANDBOXD_AGENT', 'SANDBOXD_MODEL', 'BRIDGE_PUBLIC_URL', 'CAPTURE_SERVICE_SOCKET',
    'CAPTURE_DENY_CIDRS', 'SANDBOXD_PREVIEW_ORIGIN',
}


def read_bounded(path, limit):
    # O_NONBLOCK avoids hanging on a substituted FIFO before fstat rejects it.
    descriptor = os.open(path, os.O_RDONLY | os.O_NONBLOCK)
    with os.fdopen(descriptor, 'rb') as source:
        if not stat.S_ISREG(os.fstat(source.fileno()).st_mode):
            raise ValueError('input must be a regular file')
        data = source.read(limit + 1)
        if len(data) > limit:
            raise ValueError('input exceeds size limit')
        return data


def read_config(path):
    """Read a bounded dotenv file as data, without expansion, sourcing or execution."""
    data = read_bounded(path, 1024 * 1024)
    config = {}
    for line in data.decode('utf-8').splitlines():
        match = re.match(r'^\s*(?:export\s+)?([A-Z][A-Z0-9_]*)\s*=\s*(.*?)\s*$', line)
        if not match or match[1] not in CONFIG_KEYS:
            continue
        value = match[2]
        if len(value) >= 2 and value[0] == value[-1] and value[0] in "\"'":
            value = value[1:-1]
        config[match[1]] = value
    return config


def valid_url(value, https=False, origin=True):
    try:
        if not isinstance(value, str) or re.search(r'[\s\\]', value):
            return False
        url = urlsplit(value)
        _ = url.port
        return bool(url.hostname and url.scheme in (('https',) if https else ('http', 'https'))
                    and not url.username and not url.password and not url.query and not url.fragment
                    and (not origin or url.path in ('', '/')))
    except ValueError:
        return False


def audit(runtime, platform, plan, base):
    findings = []
    def issue(code, message):
        findings.append({'code': code, 'message': message})

    # This is a source-level capability limit, deliberately not overridable by
    # an operator boolean or a supplied evidence document. Updating it requires
    # a separately reviewed networking implementation and acceptance process.
    issue('guest_egress_unavailable', 'Current OperatorEgressPolicy denies all outbound traffic and rejects domain allowances. Global model, bridge, registry and external-backend connectivity is not implemented for release.')
    if runtime.get('SANDBOXD_CUBE_EGRESS_ALLOW_DOMAINS', '').strip():
        issue('unsupported_domain_allowance', 'Configured domain allowances are rejected by the current runtime; they cannot enable connectivity.')
    if runtime.get('SANDBOXD_CUBE_ENABLED') != 'true':
        issue('cube_disabled', 'Cube is not enabled in the supplied runtime configuration.')
    if runtime.get('SANDBOXD_CUBE_ROLLOUT') != 'global':
        issue('not_global_admission', 'Configuration is not global creation mode; changing admission does not migrate existing projects.')
    if runtime.get('SANDBOXD_CUBE_APP_IDS', '').strip():
        issue('conflicting_allowlist', 'Global creation mode must not retain a pilot app allowlist.')
    for key in ('SANDBOXD_CUBE_API_URL', 'SANDBOXD_CUBE_PROXY_URL', 'SANDBOXD_AGENT_PROXY_URL'):
        if not valid_url(runtime.get(key, '')):
            issue('invalid_' + key.lower(), key + ' must be an operator-controlled HTTP(S) origin.')
    if runtime.get('SANDBOXD_API_AUTH_DISABLED') != 'false':
        issue('management_auth_not_enforced', 'SANDBOXD_API_AUTH_DISABLED must be explicitly false for rollout.')
    for key in ('SANDBOXD_CUBE_API_KEY', 'SANDBOXD_PREVIEW_TOKEN_SECRETS'):
        if not runtime.get(key, '').strip():
            issue('missing_' + key.lower(), key + ' is absent; values are never printed by this audit.')
    try:
        templates = json.loads(runtime.get('SANDBOXD_CUBE_TEMPLATES', '{}'))
        if not isinstance(templates, dict): raise ValueError()
        missing = sorted(name for name in PRESETS if not isinstance(templates.get(name), str)
                         or not re.fullmatch(r'[A-Za-z0-9_-]+', templates[name]))
        if missing: issue('missing_templates', 'Missing valid template mappings: ' + ', '.join(missing))
    except (ValueError, TypeError):
        issue('invalid_templates', 'Template mappings must be a JSON object.')

    agent = platform.get('SANDBOXD_AGENT', '').strip() or 'claude-code'
    required = plan.get('required_agents', [agent])
    if not isinstance(required, list) or any(name != 'claude-code' for name in required) or agent != 'claude-code':
        issue('unsupported_model_agent', 'Only claude-code has a Cube relay implementation; inventory all required agent overrides.')
    if not valid_url(runtime.get('SANDBOXD_CUBE_AGENT_RELAY_ORIGIN', ''), https=True):
        issue('relay_tls_missing', 'The model relay needs a valid HTTPS origin and deployed DNS/TLS routing.')
    if runtime.get('SANDBOXD_CUBE_AGENT_RELAY_NETWORK_VERIFIED') != 'true':
        issue('relay_attestation_missing', 'The relay deployment attestation is absent; setting it is not security proof and does not open networking.')
    if not valid_url(platform.get('BRIDGE_PUBLIC_URL', ''), https=True, origin=False):
        issue('bridge_route_missing', 'BRIDGE_PUBLIC_URL needs an explicit HTTPS route; host.docker.internal defaults do not provide Cube connectivity.')
    preview = platform.get('SANDBOXD_PREVIEW_ORIGIN', '').replace('%ID%', 'reviewed-project')
    if not valid_url(preview, https=True):
        issue('preview_https_missing', 'Provide the reviewed HTTPS preview origin; browser cookie, CSRF and log-redaction acceptance remains separate.')
    if not platform.get('CAPTURE_SERVICE_SOCKET', '').startswith('/'):
        issue('capture_socket_missing', 'The unified capture Unix socket must be configured and pass its read-only readiness probe.')

    try:
        inventory = plan.get('protected_addresses', [])
        if not isinstance(inventory, list): raise ValueError()
        addresses = [ipaddress.ip_address(value) for value in inventory]
        addresses = [address.ipv4_mapped or address if isinstance(address, ipaddress.IPv6Address)
                     else address for address in addresses]
        if not addresses: raise ValueError()
        networks = [ipaddress.ip_network(value.strip(), strict=False)
                    for value in platform.get('CAPTURE_DENY_CIDRS', '').split(',') if value.strip()]
        public = [address for address in addresses if address.is_global]
        if any(not any(address.version == network.version and address in network for network in networks)
               for address in public):
            issue('capture_management_exclusion_missing', 'Capture denial CIDRs do not cover every supplied public management/worker/NAT address.')
    except (ValueError, TypeError):
        issue('invalid_protected_addresses', 'Supply the management/worker/NAT address inventory and valid capture denial CIDRs.')

    capacity = plan.get('capacity', {})
    resource_summary = {}
    try:
        if not isinstance(capacity, dict): raise ValueError()
        fields = ('host_memory_mib', 'reserved_memory_mib', 'running_guests', 'peak_waking_guests',
                  'guest_memory_mib', 'free_storage_bytes', 'project_export_bytes',
                  'rollback_reserve_bytes', 'backup_staging_bytes', 'planned_snapshot_growth_bytes')
        if any(type(capacity.get(key)) is not int or capacity[key] < 0 for key in fields): raise ValueError()
        if not capacity['guest_memory_mib'] or not capacity['host_memory_mib']: raise ValueError()
        capture_workers = capacity.get('capture_workers', 2)
        if type(capture_workers) is not int or not 1 <= capture_workers <= 8: raise ValueError()
        memory = capacity['reserved_memory_mib'] + (capacity['running_guests'] + capacity['peak_waking_guests']) * capacity['guest_memory_mib'] + capture_workers * 768 + 512
        storage = sum(capacity[key] for key in ('project_export_bytes', 'rollback_reserve_bytes', 'backup_staging_bytes', 'planned_snapshot_growth_bytes'))
        resource_summary = {'minimum_planned_memory_mib': memory, 'minimum_storage_reserve_bytes': storage,
                            'capture_workers': capture_workers}
        if memory > capacity['host_memory_mib']: issue('memory_overcommitted', 'Planned guests, waking burst, capture workers and host reserve exceed available memory.')
        if storage > capacity['free_storage_bytes']: issue('storage_overcommitted', 'Exports, rollback, backups and snapshot growth exceed available storage.')
    except (ValueError, TypeError):
        issue('capacity_inventory_missing', 'Provide nonnegative measured resource and transfer-size inventory; small fixture timings are not fleet capacity proof.')

    attachments = []
    entries = plan.get('evidence', {})
    if not isinstance(entries, dict): entries = {}
    base = Path(base).resolve()
    for name in EVIDENCE:
        entry = entries.get(name, {})
        try:
            if not isinstance(entry, dict) or not re.fullmatch(r'[a-f0-9]{64}', entry.get('sha256', '')): raise ValueError()
            path = (base / entry['path']).resolve()
            if not path.is_relative_to(base) or not path.is_file() or path.stat().st_size > 2 * 1024 * 1024: raise ValueError()
            digest = hashlib.sha256(read_bounded(path, 2 * 1024 * 1024)).hexdigest()
            if digest != entry['sha256']: raise ValueError()
            attachments.append({'kind': name, 'sha256': digest, 'status': 'bytes_verified_review_required'})
        except (KeyError, TypeError, ValueError, OSError):
            issue('evidence_' + name, 'Missing or mismatched review artifact for ' + name + '; a boolean cannot substitute for deployment evidence.')

    return {'schema_version': 1, 'status': 'blocked', 'authorizes_rollout': False,
            'network_actions_performed': False, 'configuration_values_redacted': True,
            'required_default_agent': agent, 'findings': findings,
            'resource_summary': resource_summary, 'evidence_attachments': attachments,
            'scope': 'Offline inventory only. Artifact digests establish bytes, not security acceptance. No live packet testing, egress changes, service mutation or model request is performed.'}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--runtime-env', required=True)
    parser.add_argument('--platform-env', required=True)
    parser.add_argument('--plan', required=True)
    args = parser.parse_args()
    try:
        path = Path(args.plan)
        data = read_bounded(path, 1024 * 1024)
        plan = json.loads(data)
        if not isinstance(plan, dict): raise ValueError('plan must be an object')
        result = audit(read_config(args.runtime_env), read_config(args.platform_env), plan, path.parent)
        print(json.dumps(result, indent=2))
        return 2
    except (OSError, ValueError, TypeError):
        print(json.dumps({'status': 'invalid_input', 'authorizes_rollout': False,
                          'message': 'Inputs could not be read or validated; configuration contents are not echoed.'}))
        return 1


if __name__ == '__main__':
    sys.exit(main())
