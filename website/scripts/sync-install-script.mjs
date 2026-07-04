import {copyFileSync, chmodSync, mkdirSync} from 'node:fs';
import {dirname, resolve} from 'node:path';
import {fileURLToPath} from 'node:url';

const websiteRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const repoRoot = resolve(websiteRoot, '..');
const scripts = [
  {source: resolve(repoRoot, 'tools/install.sh'), target: resolve(websiteRoot, 'static/install.sh'), mode: 0o755},
  {source: resolve(repoRoot, 'tools/install.ps1'), target: resolve(websiteRoot, 'static/install.ps1'), mode: 0o644},
];

for (const {source, target, mode} of scripts) {
  mkdirSync(dirname(target), {recursive: true});
  copyFileSync(source, target);
  chmodSync(target, mode);
  console.log(`synced ${source} -> ${target}`);
}
