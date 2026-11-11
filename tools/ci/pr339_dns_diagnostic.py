"""Disposable CI diagnostics; leaves runner DNS and application source unchanged."""
import ipaddress
import json
import os
from pathlib import Path
import platform
import subprocess
import time
import uuid

OUT = Path(os.environ['RUNNER_TEMP']) / 'pr339-dns-diagnostic'
OUT.mkdir()
RECORDS = []
ARCH = {'x86_64': 'amd64', 'aarch64': 'arm64'}[platform.machine()]
IMAGES = {
    'amd64': {
        'ubuntu': 'docker.io/library/ubuntu:26.04@sha256:7b202b0e2e0028c6250f5fcf41d04df492d145a1654c6995a6553f0c1f6f1960',
        'debian': 'docker.io/library/debian:12-slim@sha256:362e64223cc0da95422b3b13c045186fc0a81250e765d31c025fbddf257f6143',
    },
    'arm64': {
        'ubuntu': 'docker.io/library/ubuntu:26.04@sha256:3ad1c71aa6b7e477f7aa91445a5442fbb163742849393b511661a88081c3d7a2',
        'debian': 'docker.io/library/debian:12-slim@sha256:a1b86db52ce3daef089e45aabe36dfec4091f82464c25c1fdcf03de197cbe82a',
    },
}
HOSTS = ['ports.ubuntu.com', 'archive.ubuntu.com', 'security.ubuntu.com', 'deb.debian.org']


def run(label, args, timeout=45):
    started = time.time()
    print('\nCOMMAND ' + label + ': ' + json.dumps(args), flush=True)
    try:
        p = subprocess.run(args, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=timeout)
        code, output = p.returncode, p.stdout.decode(errors='replace')
    except subprocess.TimeoutExpired as e:
        code, output = 'timeout', (e.stdout or b'').decode(errors='replace')
    record = dict(label=label, args=args, exit_code=code, elapsed=time.time()-started, output=output)
    RECORDS.append(record)
    (OUT / 'commands.json').write_text(json.dumps(RECORDS, indent=2) + '\n')
    print(output, end='' if output.endswith('\n') else '\n', flush=True)
    print('EXIT ' + str(code), flush=True)
    return record


run('host-resolv', ['cat', '/etc/resolv.conf'])
run('host-systemd-dns', ['resolvectl', 'dns'])
run('docker-version', ['docker', 'version'])
run('docker-info', ['docker', 'info', '--format', '{{json .ServerVersion}} {{json .KernelVersion}} {{json .OperatingSystem}} {{json .Driver}}'])
for host in HOSTS:
    run('host-' + host, ['dig', '+time=2', '+tries=1', host, 'A'])

for distro, image in IMAGES[ARCH].items():
    if run('pull-' + distro, ['docker', 'pull', '--platform', 'linux/' + ARCH, image], 120)['exit_code'] != 0:
        continue
    variants = ['default'] if distro == 'debian' else ['default', 'cloudflare-diagnostic-only']
    for variant in variants:
        case = distro + '-' + variant
        scratch = OUT / case
        scratch.mkdir(mode=0o700)
        for child in ['lists', 'lists/partial', 'archives', 'archives/partial', 'output']:
            (scratch / child).mkdir(mode=0o700)
        conf = scratch / 'apt.conf'
        conf.write_text('Acquire::Languages "none";\nDebug::NoLocking "1";\nDpkg::Use-Pty "0";\n')
        conf.chmod(0o600)
        name = 'reploy-dns-diag-' + uuid.uuid4().hex[:12]
        args = ['docker', 'create', '--name', name, '--platform', 'linux/' + ARCH,
                '--pull', 'never', '--network', 'default', '--read-only', '--user', '0:0',
                '--workdir', '/', '--mount', f'type=bind,source={scratch},target=/tmp/reploy-apt-resolve',
                '--entrypoint', '/bin/sleep']
        if variant != 'default':
            args += ['--dns', '1.1.1.1']
        args += [image, 'infinity']
        if run(case + '-create', args)['exit_code'] != 0:
            continue
        try:
            if run(case + '-start', ['docker', 'start', name])['exit_code'] != 0:
                continue
            run(case + '-inspect', ['docker', 'inspect', '--format',
                '{{json .ResolvConfPath}} {{json .HostConfig.NetworkMode}} {{json .HostConfig.Dns}} {{json .NetworkSettings.Networks}}', name])
            resolv = run(case + '-resolv', ['docker', 'exec', name, 'cat', '/etc/resolv.conf'])['output']
            run(case + '-nss', ['docker', 'exec', name, 'cat', '/etc/nsswitch.conf'])
            run(case + '-versions', ['docker', 'exec', name, '/bin/sh', '-c', 'apt-get --version; ldd --version'])
            for host in HOSTS:
                run(case + '-getent-' + host, ['docker', 'exec', name, 'getent', 'ahostsv4', host], 15)
            pid_record = run(case + '-pid', ['docker', 'inspect', '--format', '{{.State.Pid}}', name])
            pid = pid_record['output'].strip()
            if pid_record['exit_code'] == 0 and pid.isdecimal():
                for line in resolv.splitlines():
                    fields = line.split()
                    if len(fields) < 2 or fields[0] != 'nameserver':
                        continue
                    server = str(ipaddress.ip_address(fields[1]))
                    for host in ['ports.ubuntu.com', 'deb.debian.org']:
                        for transport in ['+notcp', '+tcp']:
                            run(case + '-raw-dns-' + host + transport,
                                ['sudo', '-n', 'nsenter', '-t', pid, '-n', '--', 'dig',
                                 '+time=2', '+tries=1', transport, '@' + server, host, 'A'], 8)
            env = ['docker', 'exec', '--user', '0:0', '--workdir', '/', name, '/usr/bin/env', '-i',
                   'APT_CONFIG=/tmp/reploy-apt-resolve/apt.conf', 'DEBIAN_FRONTEND=noninteractive',
                   'HOME=/root', 'LANG=C', 'LC_ALL=C', 'PATH=/usr/sbin:/usr/bin:/sbin:/bin',
                   'TMPDIR=/tmp/reploy-apt-resolve', '/bin/sh', '-c',
                   'exec </dev/null; umask "$1"; shift; exec "$@"', 'apt-resolve-v1', '0022']
            apt = ['/usr/bin/apt-get', '-o', 'Dpkg::Use-Pty=0', '-o', 'Acquire::Languages=none',
                   '-o', 'Dir::State::lists=/tmp/reploy-apt-resolve/lists', 'update', '--error-on=any']
            result = run(case + '-apt-update', env + apt, 120)
            if result['exit_code'] != 0 and distro == 'ubuntu' and variant == 'default':
                run(case + '-apt-force-ipv4', env + apt[:1] + ['-o', 'Acquire::ForceIPv4=true'] + apt[1:], 120)
        finally:
            run(case + '-cleanup', ['docker', 'rm', '-f', name])

print('\nDiagnostics complete. Diagnostic success is not a PR CI approval.', flush=True)
