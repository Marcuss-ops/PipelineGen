import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';

import { startApiDiscovery } from '../src/scrape/api-discovery.js';

class MockPage extends EventEmitter {
  on(event, handler) {
    return super.on(event, handler);
  }

  removeListener(event, handler) {
    return super.removeListener(event, handler);
  }
}

function makeRequest({ url, method = 'GET', resourceType = 'xhr', postData = null, headers = {} }) {
  return {
    url: () => url,
    method: () => method,
    resourceType: () => resourceType,
    postData: () => postData,
    headers: () => headers,
  };
}

describe('startApiDiscovery', () => {
  test('captures only Artlist xhr/fetch requests and strips sensitive headers', () => {
    const page = new MockPage();
    const discovery = startApiDiscovery(page, { log: () => {} });

    const req = makeRequest({
      url: 'https://artlist.io/graphql',
      method: 'POST',
      resourceType: 'xhr',
      postData: JSON.stringify({ operationName: 'SearchFootage' }),
      headers: {
        authorization: 'Bearer secret',
        cookie: 'session=secret',
        accept: 'application/json',
        'x-csrf-token': 'csrf',
      },
    });
    page.emit('request', req);
    page.emit('request', makeRequest({ url: 'https://example.com/x', resourceType: 'xhr' }));
    page.emit('request', makeRequest({ url: 'https://artlist.io/ignored', resourceType: 'document' }));

    const records = discovery.stop();
    assert.equal(records.length, 1);
    assert.equal(records[0].url, 'https://artlist.io/graphql');
    assert.equal(records[0].method, 'POST');
    assert.equal(records[0].post_data, JSON.stringify({ operationName: 'SearchFootage' }));
    assert.ok(!('authorization' in records[0].headers));
    assert.ok(!('cookie' in records[0].headers));
    assert.ok(!('x-csrf-token' in records[0].headers));
    assert.equal(records[0].headers.accept, 'application/json');
  });

  test('stop detaches the request listener', () => {
    const page = new MockPage();
    const discovery = startApiDiscovery(page, { log: () => {} });
    discovery.stop();
    page.emit('request', makeRequest({ url: 'https://artlist.io/graphql' }));
    assert.equal(discovery.records.length, 0);
  });
});
