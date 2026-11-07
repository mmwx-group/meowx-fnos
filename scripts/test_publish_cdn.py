"""publish_cdn.py 的纯函数测试：python3 -m unittest scripts/test_publish_cdn.py"""

import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import publish_cdn as p  # noqa: E402


class PublishCDNTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.dir = Path(self.tmp.name)

    def tearDown(self):
        self.tmp.cleanup()

    def fpk(self, name, data=b'x' * 10):
        path = self.dir / name
        path.write_bytes(data)
        return path

    def test_file_entry(self):
        f = p.file_entry(self.fpk('meowx-fnos-0.1.0-arm.fpk', b'abc'), '0.1.0')
        self.assertEqual(p.file_entry(self.fpk('meowx-fnos-0.2.0-pre1-x86.fpk'), '0.2.0-pre1')['arch'], 'x86_64')
        self.assertEqual(f['arch'], 'arm64')
        self.assertEqual(f['kind'], 'fpk')
        self.assertEqual(f['size'], 3)
        self.assertEqual(f['url'], 'https://dl.miaomiaowux.com/meowx/fnos/meowx-fnos-0.1.0-arm.fpk')
        self.assertEqual(f['sha256'], 'ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad')

    def test_file_entry_rejects_wrong_name_or_version(self):
        with self.assertRaises(SystemExit):
            p.file_entry(self.fpk('MeowX-0.1.0-fnos-x86.fpk'), '0.1.0')
        with self.assertRaises(SystemExit):
            p.file_entry(self.fpk('meowx-fnos-0.1.1-x86.fpk'), '0.1.0')

    def test_entry_orders_x86_first(self):
        files = [p.file_entry(self.fpk(f'meowx-fnos-0.1.0-{a}.fpk'), '0.1.0') for a in ('arm', 'x86')]
        e = p.fnos_entry('0.1.0', '0909705', files, 'T')
        self.assertEqual([f['arch'] for f in e['files']], ['x86_64', 'arm64'])
        self.assertEqual((e['version'], e['core'], e['releasedAt']), ('0.1.0', '0909705', 'T'))

    def test_merge_keeps_other_platforms(self):
        manifest = {'updatedAt': 'old', 'platforms': {'macos': {'version': '0.7.0'}, 'openwrt': {'version': '0.2.1'}}}
        out = p.merge_manifest(manifest, {'version': '0.1.0'}, 'now')
        self.assertEqual(out['platforms']['macos'], {'version': '0.7.0'})
        self.assertEqual(out['platforms']['openwrt'], {'version': '0.2.1'})
        self.assertEqual(out['platforms']['fnos'], {'version': '0.1.0'})
        self.assertEqual(out['updatedAt'], 'now')
        self.assertEqual(manifest['updatedAt'], 'old')  # 不改传进来的

    def test_merge_refuses_older(self):
        manifest = {'platforms': {'fnos': {'version': '0.2.0'}}}
        with self.assertRaises(SystemExit):
            p.merge_manifest(manifest, {'version': '0.1.9'}, 'now')
        self.assertEqual(p.merge_manifest(manifest, {'version': '0.2.0'}, 'now')['platforms']['fnos']['version'], '0.2.0')


if __name__ == '__main__':
    unittest.main()
