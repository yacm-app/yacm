// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 The YACM Authors. Additional terms under GPLv3 section 7: see NOTICE.md.

// Sets the version and the SHA-256 of the Windows zips in the Scoop manifest
// and the Chocolatey package after a release:
//   node build/package-managers.mjs 1.2.0 SHA256SUMS.txt
// SHA256SUMS.txt is the release file of that version.
import { readFileSync, writeFileSync } from 'node:fs';

const version = (process.argv[2] || '').replace(/^v/, '');
const sumsFile = process.argv[3];
if (!/^\d+\.\d+\.\d+$/.test(version) || !sumsFile) {
  console.error('usage: node build/package-managers.mjs <version> <SHA256SUMS.txt> (final versions only)');
  process.exit(2);
}
const sums = {};
for (const line of readFileSync(sumsFile, 'utf8').split('\n')) {
  const m = line.match(/^([0-9a-f]{64})\s+\*?(\S+)$/);
  if (m) sums[m[2]] = m[1];
}
const sum = arch => {
  const f = `yacm-${version}-windows-${arch}.zip`;
  if (!sums[f]) { console.error(`${f} is not in ${sumsFile}`); process.exit(1); }
  return sums[f];
};
const x64 = sum('x64'), arm64 = sum('arm64');
const at = p => new URL(p, import.meta.url);

const scoop = JSON.parse(readFileSync(at('scoop/yacm.json'), 'utf8'));
scoop.version = version;
for (const [key, arch, hash] of [['64bit', 'x64', x64], ['arm64', 'arm64', arm64]]) {
  const a = scoop.architecture[key];
  a.url = `https://github.com/yacm-app/yacm/releases/download/v${version}/yacm-${version}-windows-${arch}.zip`;
  a.hash = hash;
  a.extract_dir = `yacm-${version}-windows-${arch}`;
}
writeFileSync(at('scoop/yacm.json'), JSON.stringify(scoop, null, 4) + '\n');

const edit = (p, pairs) => {
  let s = readFileSync(at(p), 'utf8');
  for (const [re, to] of pairs) {
    if (!re.test(s)) { console.error(`${p}: ${re} not found`); process.exit(1); }
    s = s.replace(re, to);
  }
  writeFileSync(at(p), s);
};
edit('chocolatey/yacm.nuspec', [
  [/<version>[^<]*<\/version>/, `<version>${version}</version>`],
  [/releases\/tag\/v[^<]*</, `releases/tag/v${version}<`],
]);
edit('chocolatey/tools/chocolateyinstall.ps1', [
  [/\$version = '[^']*'/, `$version = '${version}'`],
  [/x64   = '[0-9a-f]{64}'/, `x64   = '${x64}'`],
  [/arm64 = '[0-9a-f]{64}'/, `arm64 = '${arm64}'`],
]);
console.log(`Scoop and Chocolatey files set to ${version}.`);
