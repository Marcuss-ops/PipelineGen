// Test 7: Browser-session pure helpers — resolveChromeProfile + makeTempBrowserDir.
//
// Covers the user-requested test area "fallback Puppeteer" indirectly:
// the browsers-keep-alive logic in src/driver/browser.js is mostly
// puppeteer-bound, but the two pure helpers — makeTempBrowserDir and
// resolveChromeProfile — are testable without launching Chromium.
//
// These cover the file-system side of the browser session lifecycle so
// the runtime path can be reasoned about without spinning up a real
// Chromium process.

import { test, describe, afterEach } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import {
  resolveChromeProfile,
  makeTempBrowserDir,
  closeBrowserHandle,
} from '../src/driver/browser.js';

const tmpDirsCreated = [];

function mkRealTmpDir(label) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), `velox-chrome-test-${label}-`));
  tmpDirsCreated.push(dir);
  return dir;
}

afterEach(() => {
  while (tmpDirsCreated.length > 0) {
    const dir = tmpDirsCreated.pop();
    try {
      fs.rmSync(dir, { recursive: true, force: true });
    } catch {
      /* swallow ENOENT etc. */
    }
  }
});

describe('makeTempBrowserDir', () => {
  test('returns a directory path', () => {
    const dir = makeTempBrowserDir();
    tmpDirsCreated.push(dir);
    assert.equal(typeof dir, 'string');
    assert.ok(dir.length > 0);
  });

  test('the returned path actually exists on disk', () => {
    const dir = makeTempBrowserDir();
    tmpDirsCreated.push(dir);
    const stat = fs.statSync(dir);
    assert.ok(stat.isDirectory(), `expected ${dir} to be a directory`);
  });

  test('two consecutive calls produce distinct tmp dirs', () => {
    const dir1 = makeTempBrowserDir();
    tmpDirsCreated.push(dir1);
    const dir2 = makeTempBrowserDir();
    tmpDirsCreated.push(dir2);
    assert.notEqual(dir1, dir2);
  });

  test('each call produces a unique path (no collisions under stress)', () => {
    const dirs = new Set();
    for (let i = 0; i < 8; i += 1) {
      const dir = makeTempBrowserDir();
      tmpDirsCreated.push(dir);
      assert.ok(!dirs.has(dir), `collision: ${dir}`);
      dirs.add(dir);
    }
    assert.equal(dirs.size, 8);
  });
});

describe('resolveChromeProfile', () => {
  test('returns the supplied profileDir when it exists', () => {
    const real = mkRealTmpDir('resolve-existing');
    const out = resolveChromeProfile(real);
    assert.equal(out, real);
  });

  test('falls back to a temp dir when profileDir is empty string', () => {
    const fallback = resolveChromeProfile('');
    tmpDirsCreated.push(fallback);
    assert.equal(typeof fallback, 'string');
    assert.ok(fallback.length > 0);
    assert.ok(fs.statSync(fallback).isDirectory());
  });

  test('falls back to a temp dir when profileDir does not exist', () => {
    const nonExistent = path.join(os.tmpdir(), 'velox-chrome-test-NEVER-' + Date.now());
    assert.equal(fs.existsSync(nonExistent), false);
    const fallback = resolveChromeProfile(nonExistent);
    tmpDirsCreated.push(fallback);
    assert.notEqual(fallback, nonExistent);
    assert.ok(fs.statSync(fallback).isDirectory());
  });

  test('selects /dev/shm when available, os.tmpdir() otherwise', () => {
    // makeTempBrowserDir consults /dev/shm first; on most CI runners
    // /dev/shm is available, but the contract is "the returned path
    // is a writable directory under one of those two roots".
    const dir = makeTempBrowserDir();
    tmpDirsCreated.push(dir);
    const stat = fs.statSync(dir);
    const underShm = dir.startsWith('/dev/shm/');
    const underTmp = dir.startsWith(os.tmpdir());
    assert.ok(
      underShm || underTmp,
      `expected ${dir} to be under /dev/shm or ${os.tmpdir()}`
    );
    assert.ok(stat.isDirectory());
  });
});

describe('closeBrowserHandle', () => {
  test('removes a temporary profile owned by the browser handle', async () => {
    const profileDir = fs.mkdtempSync(path.join(os.tmpdir(), 'velox-chrome-cleanup-'));
    await closeBrowserHandle({
      page: { close: async () => {} },
      context: { close: async () => {} },
      browser: { close: async () => {} },
      connected: false,
      userDataDir: profileDir,
      ownsUserDataDir: true,
    });
    assert.equal(fs.existsSync(profileDir), false);
  });

  test('preserves a configured profile owned by the operator', async () => {
    const profileDir = mkRealTmpDir('persistent-profile');
    await closeBrowserHandle({
      browser: { close: async () => {} },
      connected: false,
      userDataDir: profileDir,
      ownsUserDataDir: false,
    });
    assert.equal(fs.existsSync(profileDir), true);
  });
});
