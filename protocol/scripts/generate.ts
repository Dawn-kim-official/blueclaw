import { mkdir, mkdtemp, rename, rm, writeFile } from 'node:fs/promises';
import { basename, dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { buildProtocolArtifacts, serializeArtifact } from '../src/artifacts.ts';

const generatedDirectory = fileURLToPath(new URL('../dist/', import.meta.url));

export async function generateProtocolArtifacts(targetDirectory = generatedDirectory): Promise<void> {
  await replaceGeneratedDirectory(targetDirectory, writeProtocolArtifacts);
}

export async function replaceGeneratedDirectory(
  targetDirectory: string,
  populateDirectory: (temporaryDirectory: string) => Promise<void>,
): Promise<void> {
  const parentDirectory = dirname(targetDirectory);
  await mkdir(parentDirectory, { recursive: true });
  const temporaryDirectory = await mkdtemp(join(parentDirectory, `.${basename(targetDirectory)}-staging-`));
  const previousDirectory = `${temporaryDirectory}-previous`;
  let hasPreviousDirectory = false;

  try {
    await populateDirectory(temporaryDirectory);
    try {
      await rename(targetDirectory, previousDirectory);
      hasPreviousDirectory = true;
    } catch (errorValue) {
      if (!isNotFoundError(errorValue)) throw errorValue;
    }
    try {
      await rename(temporaryDirectory, targetDirectory);
    } catch (errorValue) {
      if (hasPreviousDirectory) await rename(previousDirectory, targetDirectory);
      throw errorValue;
    }
    if (hasPreviousDirectory) await rm(previousDirectory, { recursive: true });
  } finally {
    await rm(temporaryDirectory, { force: true, recursive: true });
  }
}

async function writeProtocolArtifacts(targetDirectory: string): Promise<void> {
  const schemaDirectory = join(targetDirectory, 'json-schema');
  const { manifest, schemas } = buildProtocolArtifacts();
  await mkdir(schemaDirectory, { recursive: true });
  await Promise.all(schemas.map(({ fileName, schema }) => writeFile(join(schemaDirectory, fileName), serializeArtifact(schema))));
  await writeFile(join(targetDirectory, 'manifest.json'), serializeArtifact(manifest));
}

function isNotFoundError(errorValue: unknown): boolean {
  if (!errorValue || typeof errorValue !== 'object') return false;
  return 'code' in errorValue && errorValue.code === 'ENOENT';
}

if (import.meta.main) await generateProtocolArtifacts();
