#!/usr/bin/env python3
"""把飞牛版安装包传到 dl.miaomiaowux.com/meowx/fnos/，并登记进 MeowX 的发布清单 meowx/latest.json（platforms.fnos）。

清单里还有 MeowX 其它端（macos / android / windows / openwrt）的条目，由别的仓库写——这里只改 fnos 一段，其余原样保留；
用 If-Match 条件写，别的仓库同时发版时重读重试。文档站的「客户端下载」页读这份清单。

fnos 条目：
  {"version", "core", "releasedAt", "files": [{"kind": "fpk", "arch", "name", "url", "size", "sha256"}]}
arch：x86_64（安装包文件名里的平台 x86）/ arm64（arm），x86_64 排前面。安装包名 meowx-fnos-<版本>-<x86|arm>.fpk。

依赖：python3 + aws cli（GitHub 托管 runner 自带）。环境变量：R2_ACCOUNT_ID / R2_ACCESS_KEY_ID / R2_SECRET_ACCESS_KEY，
可选 R2_BUCKET（默认 mmwx-dl）。

用法：
  python3 scripts/publish_cdn.py --version 0.1.0 --core 0909705 dist/*.fpk
  python3 scripts/publish_cdn.py --version 0.1.0 --core 0909705 --dry-run dist/*.fpk   # 只打印清单，不传不写
"""

from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import json
import os
import re
import subprocess
import tempfile
import time
import urllib.request
from pathlib import Path

PUBLIC_BASE = 'https://dl.miaomiaowux.com'
PREFIX = 'meowx'
MANIFEST_KEY = f'{PREFIX}/latest.json'
DIR = 'fnos'
PLATFORM = 'fnos'
# 安装包文件名里的平台（fnOS 的叫法）→ 清单里的架构
ARCHES = {'x86': 'x86_64', 'arm': 'arm64'}
NAME_RE = re.compile(r'^meowx-fnos-(?P<version>.+)-(?P<platform>x86|arm)\.fpk$')


def version_key(v: str) -> tuple[int, ...]:
    return tuple(int(x) for x in re.findall(r'\d+', v.split('+')[0]))


def file_entry(path: Path, version: str) -> dict:
    m = NAME_RE.match(path.name)
    if not m:
        raise SystemExit(f'认不出的安装包文件名：{path.name}（应为 meowx-fnos-<版本>-<x86|arm>.fpk）')
    if m['version'] != version:
        raise SystemExit(f'{path.name} 的版本与 --version {version} 不一致')
    data = path.read_bytes()
    return {
        'kind': 'fpk',
        'arch': ARCHES[m['platform']],
        'name': path.name,
        'url': f'{PUBLIC_BASE}/{PREFIX}/{DIR}/{path.name}',
        'size': len(data),
        'sha256': hashlib.sha256(data).hexdigest(),
    }


def fnos_entry(version: str, core: str, files: list[dict], now: str) -> dict:
    order = list(ARCHES.values())
    return {
        'version': version,
        'core': core,
        'releasedAt': now,
        'files': sorted(files, key=lambda f: order.index(f['arch'])),
    }


def merge_manifest(manifest: dict, entry: dict, now: str) -> dict:
    """并进 fnos 一段；比线上旧的版本拒绝（避免在老提交上手动发布把所有人「更新」回旧版）。"""
    platforms = dict(manifest.get('platforms') or {})
    current = str((platforms.get(PLATFORM) or {}).get('version') or '')
    if current and version_key(entry['version']) < version_key(current):
        raise SystemExit(f'{PLATFORM}: 线上已是 {current}，不发布更旧的 {entry["version"]}')
    platforms[PLATFORM] = entry
    return {**manifest, 'platforms': platforms, 'updatedAt': now}


class R2:
    def __init__(self) -> None:
        missing = [k for k in ('R2_ACCOUNT_ID', 'R2_ACCESS_KEY_ID', 'R2_SECRET_ACCESS_KEY') if not os.environ.get(k)]
        if missing:
            raise SystemExit(f'缺少仓库 secret：{", ".join(missing)}')
        self.bucket = os.environ.get('R2_BUCKET') or 'mmwx-dl'
        self.endpoint = f"https://{os.environ['R2_ACCOUNT_ID']}.r2.cloudflarestorage.com"
        self.env = {
            **os.environ,
            'AWS_ACCESS_KEY_ID': os.environ['R2_ACCESS_KEY_ID'],
            'AWS_SECRET_ACCESS_KEY': os.environ['R2_SECRET_ACCESS_KEY'],
            'AWS_DEFAULT_REGION': 'auto',
            # aws cli 2.23 起默认带 CRC 校验头，R2 不一定认；只在 API 要求时才算
            'AWS_REQUEST_CHECKSUM_CALCULATION': 'when_required',
            'AWS_RESPONSE_CHECKSUM_VALIDATION': 'when_required',
        }

    def _aws(self, *args: str) -> subprocess.CompletedProcess:
        return subprocess.run(['aws', '--endpoint-url', self.endpoint, *args], env=self.env, capture_output=True, text=True)

    def upload(self, path: Path) -> None:
        # 带版本号的安装包内容不会变：长缓存
        r = self._aws('s3', 'cp', str(path), f's3://{self.bucket}/{PREFIX}/{DIR}/{path.name}',
                      '--content-type', 'application/octet-stream',
                      '--cache-control', 'public, max-age=31536000, immutable')
        if r.returncode != 0:
            raise SystemExit(f'上传 {path.name} 失败：{r.stderr.strip()}')

    def get_manifest(self) -> tuple[dict, str | None]:
        with tempfile.TemporaryDirectory() as tmp:
            out = Path(tmp) / 'latest.json'
            r = self._aws('s3api', 'get-object', '--bucket', self.bucket, '--key', MANIFEST_KEY, str(out))
            if r.returncode != 0:
                if 'NoSuchKey' in r.stderr or 'Not Found' in r.stderr:
                    return {}, None
                raise SystemExit(f'读取 {MANIFEST_KEY} 失败：{r.stderr.strip()}')
            etag = json.loads(r.stdout).get('ETag')
            return json.loads(out.read_text(encoding='utf-8')), etag

    def put_manifest(self, manifest: dict, etag: str | None) -> bool:
        """条件写：线上被别人改过（ETag 变了）返回 False 让调用方重读重试。"""
        with tempfile.TemporaryDirectory() as tmp:
            out = Path(tmp) / 'latest.json'
            out.write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
            cond = ['--if-match', etag] if etag else ['--if-none-match', '*']
            r = self._aws('s3api', 'put-object', '--bucket', self.bucket, '--key', MANIFEST_KEY, '--body', str(out),
                          '--content-type', 'application/json', '--cache-control', 'no-cache', *cond)
            if r.returncode == 0:
                return True
            if 'PreconditionFailed' in r.stderr or '412' in r.stderr:
                return False
            raise SystemExit(f'写 {MANIFEST_KEY} 失败：{r.stderr.strip()}')


def _open(url: str, timeout: int):
    # Cloudflare 会拦 Python-urllib 默认 UA（403），带一个明确的 UA
    req = urllib.request.Request(f'{url}?t={int(time.time())}', headers={'User-Agent': 'MeowX-publish/1.0'})
    return urllib.request.urlopen(req, timeout=timeout)


def fetch_public_manifest() -> dict:
    with _open(f'{PUBLIC_BASE}/{MANIFEST_KEY}', 30) as resp:
        return json.load(resp)


def verify_public(files: list[dict]) -> None:
    """传完回读公开地址：大小对得上才算发出去了。"""
    for f in files:
        req = urllib.request.Request(f['url'], method='HEAD', headers={'User-Agent': 'MeowX-publish/1.0'})
        with urllib.request.urlopen(req, timeout=60) as resp:
            size = int(resp.headers.get('Content-Length') or -1)
        if size != f['size']:
            raise SystemExit(f'{f["url"]} 回读大小 {size}，应为 {f["size"]}')


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument('--version', required=True)
    ap.add_argument('--core', required=True)
    ap.add_argument('--dry-run', action='store_true')
    ap.add_argument('files', nargs='+', type=Path)
    args = ap.parse_args()

    now = dt.datetime.now(dt.timezone.utc).strftime('%Y-%m-%dT%H:%M:%SZ')
    files = [file_entry(p, args.version) for p in args.files]
    arches = [f['arch'] for f in files]
    if len(set(arches)) != len(arches):
        raise SystemExit(f'同一架构有多个安装包：{arches}')
    entry = fnos_entry(args.version, args.core, files, now)

    if args.dry_run:
        print(json.dumps(merge_manifest(fetch_public_manifest(), entry, now), ensure_ascii=False, indent=2))
        return

    r2 = R2()
    # 先确认不会把清单改回旧版，再传包
    merge_manifest(r2.get_manifest()[0], entry, now)
    for p in args.files:
        r2.upload(p)
        print(f'已上传 {PREFIX}/{DIR}/{p.name}')
    verify_public(files)
    for attempt in range(6):
        manifest, etag = r2.get_manifest()
        manifest = merge_manifest(manifest, entry, now)
        if r2.put_manifest(manifest, etag):
            break
        print('latest.json 被并发修改，重读重试')
        time.sleep(2 + attempt * 2)
    else:
        raise SystemExit('latest.json 多次条件写冲突，放弃')
    print(f'已更新 {MANIFEST_KEY}：{PLATFORM} {args.version}')


if __name__ == '__main__':
    main()
