import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';

import {
  normalizeSegmentConcurrency,
  spoolSegmentsToFile,
} from '../src/server/segment-spool.js';

test('normalizeSegmentConcurrency applies fallback and cap', () => {
  assert.equal(normalizeSegmentConcurrency(undefined), 5);
  assert.equal(normalizeSegmentConcurrency('0'), 5);
  assert.equal(normalizeSegmentConcurrency('8'), 8);
  assert.equal(normalizeSegmentConcurrency('999'), 16);
});

test('spoolSegmentsToFile preserves playlist order and cleans spool files', async () => {
  const tempDir = await fs.promises.mkdtemp(path.join(os.tmpdir(), 'segment-spool-test-'));
  const outputPath = path.join(tempDir, 'video.ts');
  const urls = ['segment-0', 'segment-1', 'segment-2', 'segment-3'];
  let active = 0;
  let peak = 0;

  try {
    await spoolSegmentsToFile({
      segmentUrls: urls,
      outputPath,
      concurrency: 2,
      downloadSegment: async (url, destination, index) => {
        active += 1;
        peak = Math.max(peak, active);
        await new Promise((resolve) => setTimeout(resolve, (urls.length - index) * 5));
        await fs.promises.writeFile(destination, `${url}\n`);
        active -= 1;
      },
    });

    assert.equal(
      await fs.promises.readFile(outputPath, 'utf8'),
      'segment-0\nsegment-1\nsegment-2\nsegment-3\n',
    );
    assert.equal(peak, 2);
    const leftovers = (await fs.promises.readdir(tempDir)).filter((name) => name.startsWith('.hls-'));
    assert.deepEqual(leftovers, []);
  } finally {
    await fs.promises.rm(tempDir, { recursive: true, force: true });
  }
});

test('spoolSegmentsToFile removes partial output after a segment failure', async () => {
  const tempDir = await fs.promises.mkdtemp(path.join(os.tmpdir(), 'segment-spool-failure-'));
  const outputPath = path.join(tempDir, 'video.ts');

  try {
    await assert.rejects(
      spoolSegmentsToFile({
        segmentUrls: ['ok', 'broken'],
        outputPath,
        concurrency: 2,
        downloadSegment: async (url, destination) => {
          if (url === 'broken') {
            throw new Error('segment failed');
          }
          await fs.promises.writeFile(destination, 'ok');
        },
      }),
      /segment failed/,
    );
    assert.equal(fs.existsSync(outputPath), false);
    const leftovers = (await fs.promises.readdir(tempDir)).filter((name) => name.startsWith('.hls-'));
    assert.deepEqual(leftovers, []);
  } finally {
    await fs.promises.rm(tempDir, { recursive: true, force: true });
  }
});
