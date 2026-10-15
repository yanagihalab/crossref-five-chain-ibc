import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { createHash, randomUUID } from 'node:crypto';
import { parseArgs } from 'node:util';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('../../', import.meta.url));
const hash = bytes => createHash('sha256').update(bytes).digest('hex');
const write = (file, value) => {
  const fd = fs.openSync(file, 'wx', 0o600);
  try { fs.writeFileSync(fd, value); fs.fsyncSync(fd); } finally { fs.closeSync(fd); }
};

export function prepareLocalSecrets(binary, destination, execute = execFileSync) {
  const dir = path.resolve(destination);
  if (fs.existsSync(dir)) throw new Error('Local identities already exist; never overwrite or regenerate an existing experiment');
  const parent = path.dirname(dir);
  fs.mkdirSync(parent, { recursive: true, mode: 0o700 });
  if (fs.realpathSync(parent) !== parent) throw new Error('Secret directory aliases are refused');
  const staging = dir + '.prepare-' + randomUUID();
  fs.mkdirSync(staging, { mode: 0o700 });
  const manifest = { kind: 'new-local-test-identities', fileSHA256: {}, productionFundsAllowed: false };
  for (const name of ['validator', 'relayer']) {
    let result;
    try {
      result = JSON.parse(execute(path.resolve(binary), ['keys', 'add', name, '--home', path.join(staging, 'home'),
        '--keyring-backend', 'test', '--output', 'json'], { cwd: root, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'], timeout: 60000 }));
    } catch { throw new Error('Local test identity generation failed; secret-bearing output suppressed; partial files retained'); }
    if (typeof result.mnemonic !== 'string' || ![12, 15, 18, 21, 24].includes(result.mnemonic.split(' ').length)) {
      throw new Error('Unexpected identity response; partial files retained');
    }
    const bytes = result.mnemonic.trim() + '\n';
    write(path.join(staging, name + '.mnemonic'), bytes);
    manifest.fileSHA256[name + '.mnemonic'] = hash(bytes);
  }
  write(path.join(staging, 'manifest.json'), JSON.stringify(manifest, null, 2) + '\n');
  // rename is not used: an independently created destination must not be replaced.
  fs.mkdirSync(dir, { mode: 0o700 });
  for (const name of ['validator.mnemonic', 'relayer.mnemonic', 'manifest.json']) {
    const bytes = fs.readFileSync(path.join(staging, name));
    write(path.join(dir, name), bytes);
    if (!fs.readFileSync(path.join(dir, name)).equals(bytes)) throw new Error('Secret readback failed; partial files retained');
  }
  return { localTestIdentitiesPrepared: true, existingIdentitiesOverwritten: false, secretValuesPrinted: false };
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const { values } = parseArgs({ options: { binary: { type: 'string', default: 'build/crossrefdd' } } });
    console.log(JSON.stringify(prepareLocalSecrets(path.resolve(root, values.binary), path.join(root, 'docker/generated/local-secrets'))));
  } catch (error) { console.error(error.message); process.exitCode = 1; }
}
