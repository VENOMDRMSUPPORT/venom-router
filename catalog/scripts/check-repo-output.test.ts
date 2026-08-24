import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { test } from 'node:test';
import { findRepositoryLogs } from './check-repo-output.ts';

test('repository output check catches logs outside ignored build directories', () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'catalog-repo-output-'));
  try {
    fs.mkdirSync(path.join(root, 'node_modules', 'dependency'), { recursive: true });
    fs.mkdirSync(path.join(root, 'dist'), { recursive: true });
    fs.writeFileSync(path.join(root, 'node_modules', 'dependency', 'dependency.log'), 'ignored');
    fs.writeFileSync(path.join(root, 'dist', 'bundle.log'), 'ignored');
    fs.writeFileSync(path.join(root, 'gate.log'), 'unexpected output');

    assert.deepEqual(findRepositoryLogs(root), ['gate.log']);

    fs.rmSync(path.join(root, 'gate.log'));
    assert.deepEqual(findRepositoryLogs(root), []);
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
});
