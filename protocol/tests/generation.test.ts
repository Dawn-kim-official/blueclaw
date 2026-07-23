import { describe, expect, test } from 'bun:test';
import { mkdir, mkdtemp, readFile, readdir, rm, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';

import { checkProtocolArtifacts, generateProtocolArtifacts, replaceGeneratedDirectory } from '../scripts/generate.ts';

describe('protocol artifact generation', () => {
  test('replaces generated artifacts after population succeeds', async () => {
    const parentDirectory = await mkdtemp(join(tmpdir(), 'blueclaw-protocol-'));
    const generatedDirectory = join(parentDirectory, 'generated');
    await mkdir(generatedDirectory);
    await writeFile(join(generatedDirectory, 'obsolete.json'), '{}');

    try {
      await replaceGeneratedDirectory(generatedDirectory, async temporaryDirectory => {
        await writeFile(join(temporaryDirectory, 'manifest.json'), '{"version":"next"}');
      });

      expect(await readFile(join(generatedDirectory, 'manifest.json'), 'utf8')).toBe('{"version":"next"}');
      expect(await readdir(parentDirectory)).toEqual(['generated']);
    } finally {
      await rm(parentDirectory, { force: true, recursive: true });
    }
  });

  test('preserves generated artifacts when population fails', async () => {
    const parentDirectory = await mkdtemp(join(tmpdir(), 'blueclaw-protocol-'));
    const generatedDirectory = join(parentDirectory, 'generated');
    await mkdir(generatedDirectory);
    await writeFile(join(generatedDirectory, 'manifest.json'), '{"version":"current"}');

    try {
      const generation = replaceGeneratedDirectory(generatedDirectory, async temporaryDirectory => {
        await writeFile(join(temporaryDirectory, 'manifest.json'), '{"version":"partial"}');
        throw new Error('generation failed');
      });

      await expect(generation).rejects.toThrow('generation failed');
      expect(await readFile(join(generatedDirectory, 'manifest.json'), 'utf8')).toBe('{"version":"current"}');
      expect(await readdir(parentDirectory)).toEqual(['generated']);
    } finally {
      await rm(parentDirectory, { force: true, recursive: true });
    }
  });

  test('rejects stale and extra generated artifacts', async () => {
    const parentDirectory = await mkdtemp(join(tmpdir(), 'blueclaw-protocol-'));
    const generatedDirectory = join(parentDirectory, 'generated');

    try {
      await generateProtocolArtifacts(generatedDirectory);
      await expect(checkProtocolArtifacts(generatedDirectory)).resolves.toBeUndefined();

      await writeFile(join(generatedDirectory, 'capability-tools.json'), '{}');
      await expect(checkProtocolArtifacts(generatedDirectory)).rejects.toThrow('capability-tools.json');

      await writeFile(join(generatedDirectory, 'catalog.go'), 'package capabilitycatalog\n');
      await generateProtocolArtifacts(generatedDirectory);
      expect(await readFile(join(generatedDirectory, 'catalog.go'), 'utf8')).toBe('package capabilitycatalog\n');
      await expect(checkProtocolArtifacts(generatedDirectory)).resolves.toBeUndefined();

      await writeFile(join(generatedDirectory, 'extra.json'), '{}');
      await expect(checkProtocolArtifacts(generatedDirectory)).rejects.toThrow('generated protocol paths differ');
    } finally {
      await rm(parentDirectory, { force: true, recursive: true });
    }
  });
});
