// Test 1: parseArgs — CLI argument parser.
//
// Covers all branches of cli/args.js::parseArgs including:
//   - default values (no flags)
//   - --term and -t short form
//   - --limit and -l short form (with default / non-int / non-positive)
//   - --profile-dir flag
//   - unknown flags are ignored
//   - non-array argv is treated as defaults
//   - CHROME_PROFILE_DIR env var respected on default
//   - flag-pair ordering doesn't matter

import { test, describe, after } from 'node:test';
import assert from 'node:assert/strict';
import { parseArgs } from '../cli/args.js';

describe('parseArgs', () => {
  const ORIGINAL_ENV = process.env.CHROME_PROFILE_DIR;

  after(() => {
    if (ORIGINAL_ENV === undefined) {
      delete process.env.CHROME_PROFILE_DIR;
    } else {
      process.env.CHROME_PROFILE_DIR = ORIGINAL_ENV;
    }
  });

  test('returns defaults when no flags are passed', () => {
    delete process.env.CHROME_PROFILE_DIR;
    const out = parseArgs(['node', 'script']);
    assert.equal(out.term, '');
    assert.equal(out.limit, 8);
    assert.equal(out.profileDir, '');
  });

  test('parses --term long form', () => {
    delete process.env.CHROME_PROFILE_DIR;
    const out = parseArgs(['node', 'script', '--term', 'sunrise']);
    assert.equal(out.term, 'sunrise');
  });

  test('parses -t short form', () => {
    delete process.env.CHROME_PROFILE_DIR;
    const out = parseArgs(['node', 'script', '-t', 'sunset']);
    assert.equal(out.term, 'sunset');
  });

  test('parses --limit with positive int', () => {
    delete process.env.CHROME_PROFILE_DIR;
    const out = parseArgs(['node', 'script', '--limit', '20']);
    assert.equal(out.limit, 20);
  });

  test('parses -l short form with positive int', () => {
    delete process.env.CHROME_PROFILE_DIR;
    const out = parseArgs(['node', 'script', '-l', '5']);
    assert.equal(out.limit, 5);
  });

  test('--limit falls back to default for non-numeric value', () => {
    delete process.env.CHROME_PROFILE_DIR;
    const out = parseArgs(['node', 'script', '--limit', 'not-a-number']);
    assert.equal(out.limit, 8);
  });

  test('--limit falls back to default for zero / negative', () => {
    delete process.env.CHROME_PROFILE_DIR;
    const out1 = parseArgs(['node', 'script', '--limit', '0']);
    assert.equal(out1.limit, 8);
    const out2 = parseArgs(['node', 'script', '--limit', '-3']);
    assert.equal(out2.limit, 8);
  });

  test('parses --profile-dir', () => {
    delete process.env.CHROME_PROFILE_DIR;
    const out = parseArgs(['node', 'script', '--profile-dir', '/tmp/profile']);
    assert.equal(out.profileDir, '/tmp/profile');
  });

  test('falls back to CHROME_PROFILE_DIR env when --profile-dir is absent', () => {
    process.env.CHROME_PROFILE_DIR = '/env-profile';
    const out = parseArgs(['node', 'script']);
    assert.equal(out.profileDir, '/env-profile');
  });

  test('--profile-dir overrides env default', () => {
    process.env.CHROME_PROFILE_DIR = '/env-profile';
    const out = parseArgs(['node', 'script', '--profile-dir', '/flag-profile']);
    assert.equal(out.profileDir, '/flag-profile');
  });

  test('unknown flags are ignored, known ones still parse after', () => {
    delete process.env.CHROME_PROFILE_DIR;
    const out = parseArgs([
      'node',
      'script',
      '--no-such-flag',
      '--term',
      'hello',
      '-x',
    ]);
    assert.equal(out.term, 'hello');
  });

  test('non-array argv yields defaults without throwing', () => {
    delete process.env.CHROME_PROFILE_DIR;
    const out = parseArgs(null);
    assert.equal(out.term, '');
    assert.equal(out.limit, 8);
    assert.equal(out.profileDir, '');
  });

  test('--term with no following value yields empty string', () => {
    delete process.env.CHROME_PROFILE_DIR;
    const out = parseArgs(['node', 'script', '--term']);
    assert.equal(out.term, '');
  });
});
