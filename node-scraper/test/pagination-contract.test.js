import { test } from 'node:test';
import assert from 'node:assert/strict';

import { findPagination } from '../artlist/gateway-search.js';

test('pagination contract extracts authoritative provider fields', () => {
  assert.deepEqual(findPagination({ data: { search: {
    totalCount: 437, hasNextPage: true, nextPage: 2,
  } } }), { total: 437, has_next_page: true, next_page: 2 });
});

test('pagination contract stays empty when provider exposes no total', () => {
  assert.deepEqual(findPagination({ data: { results: [{ id: '1' }] } }), {});
});
