import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('../../', import.meta.url));
const deniedPath = /(^|\/)(?:\.env(?:\..*)?|secrets|hermes-keys|keyring-[^/]*|generated|\.security-deps)(?:\/|$)|\.(?:key|pem|p12|pfx|mnemonic|sparsebundle|cms)$|(?:^|\/)(?:priv_validator_key|node_key|priv_validator_state)\.json$/;
const deniedText = [
  ['private-key-block', /-----BEGIN (?:[A-Z0-9]+ )*PRIVATE KEY-----/],
  ['private-key-payload', /["']priv_key["']\s*:\s*\{[^}]*["']value["']\s*:\s*["'][A-Za-z0-9+/]{40,}={0,2}["']/],
  ['operator-home', /\/Users\/[a-zA-Z0-9_.-]+\//],
  ['operator-volume', /\/Volumes\/[a-zA-Z0-9_.-]+\//],
  ['provider-account', /\bvps\d{6,}\b/],
];

export function inspectPublicFile(name, text) {
  const failures = [];
  if (deniedPath.test(name) && name !== '.env.example') failures.push('private-runtime-path');
  for (const [rule, pattern] of deniedText) if (pattern.test(text)) failures.push(rule);
  return failures;
}

export function checkPublicSource(directory = root) {
  const files = execFileSync('git', ['ls-files', '--cached', '--others', '--exclude-standard', '-z'],
    { cwd: directory, encoding: 'utf8' }).split('\0').filter(Boolean);
  const findings = [];
  let scanned = 0;
  for (const name of files) {
    const file = path.join(directory, name);
    if (!fs.existsSync(file)) throw new Error('Missing publication source: ' + name);
    const st = fs.lstatSync(file);
    if (!st.isFile() || st.isSymbolicLink() || st.nlink !== 1) throw new Error('Unsafe publication file: ' + name);
    const bytes = fs.readFileSync(file);
    if (bytes.includes(0)) throw new Error('Unexpected binary publication file: ' + name);
    for (const rule of inspectPublicFile(name, bytes.toString('utf8'))) findings.push({ file: name, rule });
    scanned++;
  }
  if (findings.length) {
    console.error(JSON.stringify({ publicationBlocked: true, findings }, null, 2));
    return false;
  }
  console.log(JSON.stringify({ publicSourceChecked: true, files: scanned, secretValuesPrinted: false }));
  return true;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try { process.exitCode = checkPublicSource() ? 0 : 1; }
  catch (error) { console.error(error.message); process.exitCode = 1; }
}
