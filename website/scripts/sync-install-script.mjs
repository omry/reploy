import {copyFileSync, chmodSync, mkdirSync} from 'node:fs';
import {dirname, resolve} from 'node:path';
import {fileURLToPath} from 'node:url';

const websiteRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const repoRoot = resolve(websiteRoot, '..');
const source = resolve(repoRoot, 'tools/install.sh');
const target = resolve(websiteRoot, 'static/install.sh');

mkdirSync(dirname(target), {recursive: true});
copyFileSync(source, target);
chmodSync(target, 0o755);
console.log(`synced ${source} -> ${target}`);
