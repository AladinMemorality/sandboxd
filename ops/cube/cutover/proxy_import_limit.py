#!/usr/bin/env python3
"""Render a scoped Cube proxy import fix; never install or reload services."""
import argparse
import hashlib
from pathlib import Path
import re

MARKER = '# Baarcha private migration streams: bounded, authenticated, no replay.'


def render(source):
    if MARKER in source:
        raise ValueError('Already patched; inspect the installed generation')
    if source.count('client_max_body_size 256M;') != 1:
        raise ValueError('Expected existing default upload limit')
    pattern = re.compile(r'^        location / \{\n(.*?)^        \}', re.M | re.S)
    ports = []

    def replace(match):
        body = match[1]
        if 'grpc_pass ' in body:
            return match[0]  # Leave the gRPC server untouched.
        if 'rewrite_by_lua_file lua/rewrite_phase.lua;' not in body:
            raise ValueError('Missing authenticated host rewrite')
        port = re.findall(r'set \$host_proxy_port (80|443);', body)
        if len(port) != 1 or 'proxy_pass http://backend;' not in body:
            raise ValueError('Unrecognized host route')
        ports.extend(port)
        # Clone the installed authenticated host route, including its ingress
        # auth, backend resolution, response filters and access logging.
        route = ('        ' + MARKER + '\n'
                 '        location ~ ^/import/(?:private-workspace-v2|private-home)$ {\n'
                 '            client_max_body_size 4g;\n'
                 '            proxy_request_buffering off;\n'
                 '            proxy_next_upstream off;\n'
                 '            if ($request_method != PUT) { return 405; }\n'
                 '            if ($host !~ "^3031-[a-f0-9]{32}\\\\.") { return 404; }\n'
                 + body + '        }\n\n')
        return route + match[0]

    result = pattern.sub(replace, source)
    if sorted(ports) != ['443', '80']:
        raise ValueError('Exactly the HTTP and HTTPS host routes are required')
    return result


if __name__ == '__main__':
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('source', type=Path)
    p.add_argument('output', type=Path)
    p.add_argument('--source-sha256', required=True)
    args = p.parse_args()
    data = args.source.read_bytes()
    if hashlib.sha256(data).hexdigest() != args.source_sha256:
        raise SystemExit('Source generation changed')
    output = render(data.decode()).encode()
    with args.output.open('xb') as f:
        f.write(output)
    print(hashlib.sha256(output).hexdigest())
