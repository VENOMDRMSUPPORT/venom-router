import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { test } from 'node:test';
import { findCssModuleIssues, scanCssModules } from './check-css-modules.ts';

/** A throwaway component tree: one source file, one stylesheet beside it. */
function fixture(source: string, css: string): { root: string; cleanup: () => void } {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'catalog-css-modules-'));
  const sourceDir = path.join(root, 'components');
  fs.mkdirSync(sourceDir, { recursive: true });
  fs.writeFileSync(path.join(sourceDir, 'Example.tsx'), source);
  fs.writeFileSync(path.join(sourceDir, 'Example.module.css'), css);
  return { root, cleanup: () => fs.rmSync(root, { recursive: true, force: true }) };
}

const IMPORT = "import styles from './Example.module.css';\n";

test('CSS-module check fails on an undefined key and passes after the key is added', () => {
  const { root, cleanup } = fixture(
    `${IMPORT}export const Example = () => <div className={\`\${styles.good} \${styles.missing}\`} />;\n`,
    '.good { color: red; }\n',
  );
  try {
    const missing = findCssModuleIssues(root);
    assert.equal(missing.length, 1);
    assert.equal(missing[0].kind, 'missing-key');
    assert.equal(missing[0].key, 'missing');
    assert.equal(missing[0].line, 2);

    fs.appendFileSync(path.join(root, 'components', 'Example.module.css'), '.missing { color: blue; }\n');
    assert.deepEqual(findCssModuleIssues(root), []);
  } finally {
    cleanup();
  }
});

test('a templated key is checked by its static prefix, so a renamed family cannot pass', () => {
  // `signal-${severity}` can only ever name a class starting `signal-`. The keys
  // themselves are runtime values, but their existence as a family is not.
  const source = `${IMPORT}export const Example = ({ s }: { s: string }) => <div className={styles[\`signal-\${s}\`]} />;\n`;
  const renamed = fixture(source, '.tone-critical { color: red; }\n');
  try {
    const issues = findCssModuleIssues(renamed.root);
    assert.equal(issues.length, 1);
    assert.equal(issues[0].kind, 'missing-prefix');
    assert.equal(issues[0].key, 'signal-');
  } finally {
    renamed.cleanup();
  }

  const present = fixture(source, '.signal-critical { color: red; }\n');
  try {
    assert.deepEqual(findCssModuleIssues(present.root), []);
  } finally {
    present.cleanup();
  }
});

test('a runtime-keyed access is reported as unverified rather than passing in silence', () => {
  // The check cannot decide `styles[tone]` without the type of `tone`. Counting
  // it keeps the result honest: a green run states what it did not examine.
  const { root, cleanup } = fixture(
    `${IMPORT}export const Example = ({ tone }: { tone: string }) => <div className={styles[tone]} />;\n`,
    '.good { color: red; }\n',
  );
  try {
    const scan = scanCssModules(root);
    assert.deepEqual(scan.issues, [], 'unverifiable is not the same as broken');
    assert.equal(scan.unverified.length, 1);
    assert.equal(scan.unverified[0].expression, 'styles[tone]');
    assert.equal(scan.unverified[0].line, 2);
  } finally {
    cleanup();
  }
});

test('a missing stylesheet is its own finding, not a key named like one', () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'catalog-css-modules-'));
  try {
    fs.mkdirSync(path.join(root, 'components'), { recursive: true });
    fs.writeFileSync(path.join(root, 'components', 'Example.tsx'), `${IMPORT}export const Example = () => <div className={styles.good} />;\n`);

    const issues = findCssModuleIssues(root);
    assert.equal(issues.length, 1);
    assert.equal(issues[0].kind, 'missing-stylesheet');
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
});
