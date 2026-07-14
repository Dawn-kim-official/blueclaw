import { readdir, readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { buildProtocolArtifacts, serializeArtifact } from './artifacts.ts';

const generatedDirectory = fileURLToPath(new URL('../generated/', import.meta.url));
const { manifest, schemas } = buildProtocolArtifacts();
const expectedFiles = new Map([
  ['manifest.json', serializeArtifact(manifest)],
  ...schemas.map(({ fileName, schema }) => [join('json-schema', fileName), serializeArtifact(schema)] as const),
]);
const actualPaths = await listGeneratedPaths();
const expectedPaths = [...expectedFiles.keys()].sort();

if (JSON.stringify(actualPaths) !== JSON.stringify(expectedPaths)) {
  throw new Error(`Generated protocol artifact set is stale: expected [${expectedPaths.join(', ')}], actual [${actualPaths.join(', ')}]`);
}

for (const [relativePath, expectedContent] of expectedFiles) {
  const actualContent = await readFile(join(generatedDirectory, relativePath), 'utf8');
  if (actualContent !== expectedContent) {
    throw new Error(`Generated protocol artifact is stale: ${relativePath}`);
  }
}

async function listGeneratedPaths(): Promise<string[]> {
  try {
    const rootFileNames = await readdir(generatedDirectory);
    const schemaFileNames = rootFileNames.includes('json-schema')
      ? await readdir(join(generatedDirectory, 'json-schema'))
      : [];
    return [
      ...rootFileNames.filter(fileName => fileName !== 'json-schema'),
      ...schemaFileNames.map(fileName => join('json-schema', fileName)),
    ].sort();
  } catch {
    return [];
  }
}
