import { test, describe } from 'node:test';
import assert from 'node:assert/strict';

import { findLargestClipArray, normalizeArtlistClip } from '../artlist/normalize.js';

describe('normalize helpers', () => {
  test('findLargestClipArray finds nested clip arrays', () => {
    const payload = {
      data: {
        searchFootage: {
          results: [
            { id: 'a', title: 'Clip A', previewUrl: 'https://cdn/a.mp4' },
            { id: 'b', title: 'Clip B', previewUrl: 'https://cdn/b.mp4' },
          ],
        },
      },
    };

    const clips = findLargestClipArray(payload);
    assert.equal(clips.length, 2);
    assert.equal(clips[0].id, 'a');
  });

  test('normalizeArtlistClip preserves common metadata fields', () => {
    const clip = normalizeArtlistClip({
      id: 123,
      title: 'Business Team',
      description: 'Office footage',
      creator: { name: 'Artlist Creator' },
      tags: ['office', { title: 'team' }],
      categories: ['business'],
      clipPageUrl: 'https://artlist.io/stock-footage/clip/123',
      thumbnailUrl: 'https://cdn/thumb.jpg',
      previewUrl: 'https://cdn/preview.mp4',
      durationSeconds: 18,
      width: 3840,
      height: 2160,
    });

    assert.equal(clip.clip_id, '123');
    assert.equal(clip.title, 'Business Team');
    assert.equal(clip.page_url, 'https://artlist.io/stock-footage/clip/123');
    assert.equal(clip.primary_url, 'https://cdn/preview.mp4');
    assert.deepEqual(clip.tags, ['office', 'team']);
    assert.deepEqual(clip.categories, ['business']);
    assert.equal(clip.duration_seconds, 18);
    assert.equal(clip.width, 3840);
    assert.equal(clip.height, 2160);
  });

  test('normalizeArtlistClip derives clip id from page url when missing', () => {
    const clip = normalizeArtlistClip({
      title: 'Business Team',
      clipPageUrl: 'https://artlist.io/stock-footage/clip/business-team-working/46001',
      previewUrl: 'https://cdn/preview.mp4',
    });

    assert.equal(clip.clip_id, '46001');
    assert.equal(clip.id, '46001');
    assert.equal(clip.page_url, 'https://artlist.io/stock-footage/clip/business-team-working/46001');
  });
});
