import { test } from 'node:test';
import assert from 'node:assert/strict';
import { buildArtlistSearchURL } from '../artlist/search-url.js';

test('buildArtlistSearchURL is the canonical footage URL builder', () => {
  assert.equal(
    buildArtlistSearchURL('coastal road golden hour'),
    'https://artlist.io/stock-footage?terms=coastal+road+golden+hour',
  );
});
