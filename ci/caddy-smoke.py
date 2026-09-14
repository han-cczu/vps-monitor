#!/usr/bin/env python3
"""Verify access AND upstream-error log redaction in an isolated Caddy container."""
import argparse
import json
from pathlib import Path
import subprocess
import time
import urllib.error
import urllib.request
import uuid


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--context', default='desktop-linux')
    parser.add_argument('--config', default=str(Path(__file__).resolve().parents[1] / 'deploy' / 'Caddyfile'))
    args = parser.parse_args()
    docker = ['docker', '--context', args.context]
    marker = 'qa-' + uuid.uuid4().hex
    def run(*cmd):
        return subprocess.check_output(docker + list(cmd), text=True, stderr=subprocess.STDOUT, timeout=25).strip()
    container = run('run', '-d', '--label', 'vpsmon.qa=caddy-redaction', '-e', 'PANEL_DOMAIN=http://localhost',
                    '-p', '127.0.0.1::80', '--mount', f'type=bind,source={Path(args.config).resolve()},target=/etc/caddy/Caddyfile,readonly', 'caddy:2')
    assert len(container) == 64 and all(c in '0123456789abcdef' for c in container)
    try:
        endpoint = run('port', container, '80/tcp')
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        for path in ['/sub/'+marker+'?token='+marker, '/api/health?token='+marker]:
            request = urllib.request.Request('http://'+endpoint+path,
                       headers={'Host': 'localhost', 'Referer': 'http://localhost/sub/'+marker})
            for attempt in range(30):
                try:
                    with opener.open(request, timeout=10) as response:
                        raise AssertionError('fixture upstream unexpectedly available: '+str(response.status))
                except urllib.error.HTTPError as exc:
                    assert exc.code == 502
                    break
                except urllib.error.URLError:
                    if attempt == 29:
                        raise
                    time.sleep(.1)
        access = run('exec', container, 'cat', '/data/access.log')
        runtime = run('logs', container)
        assert marker not in access and marker not in runtime, 'subscription credential leaked to Caddy logs'
        assert 'handled request' in access and '/api/health' in access
        assert 'http.log.error' in runtime and '502' in runtime, 'missing upstream-error coverage'
        assert '/sub/' not in access and 'Referer' not in access and 'Referer' not in runtime
        print(json.dumps({'passed': True, 'access_redacted': True, 'upstream_error_redacted': True,
                          'caddy_version': run('exec', container, 'caddy', 'version')}, indent=2))
    finally:
        run('rm', '-f', container)


if __name__ == '__main__':
    main()
