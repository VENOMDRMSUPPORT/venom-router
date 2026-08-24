/**
 * Fail the gate when a CSS-module key used in the SPA does not exist.
 *
 * `src/vite-env.d.ts` types a stylesheet as `{ readonly [key: string]: string }`,
 * so `styles.anythingAtAll` type-checks and renders `class="undefined"`. The
 * compiler cannot catch it; this can.
 *
 * What it verifies:
 *   styles.someKey            the key exists in the imported stylesheet
 *   styles[`prefix_${x}`]     at least one key in the stylesheet starts with
 *                             `prefix_`, so a renamed or deleted family of
 *                             classes cannot pass unnoticed
 *
 * What it CANNOT verify, and reports as unverified rather than skipping in
 * silence: `styles[tone]`, `styles[item.cls]` and friends, where the key is only
 * known at runtime. Deciding those needs the type of the index expression, which
 * is a job for the compiler and not for a text scan. A gate that quietly ignored
 * them would be worse than no gate — it would read as coverage it does not have.
 */

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

export type CssModuleIssueKind = 'missing-stylesheet' | 'missing-key' | 'missing-prefix';

export interface CssModuleIssue {
  file: string;
  line: number;
  stylesheet: string;
  kind: CssModuleIssueKind;
  /** The key, or the static prefix, that resolved to nothing. */
  key: string;
}

/** An index expression whose key is only known at runtime. */
export interface CssModuleUnverified {
  file: string;
  line: number;
  expression: string;
}

export interface CssModuleScan {
  issues: CssModuleIssue[];
  unverified: CssModuleUnverified[];
}

function sourceFiles(rootDir: string): string[] {
  const files: string[] = [];
  const visit = (directory: string) => {
    for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
      if (entry.name === 'node_modules' || entry.name === 'dist') continue;
      const absolute = path.join(directory, entry.name);
      if (entry.isDirectory()) visit(absolute);
      else if (/\.(?:tsx|ts)$/.test(entry.name)) files.push(absolute);
    }
  };
  visit(rootDir);
  return files;
}

function cssKeys(stylesheet: string): Set<string> {
  const content = fs.readFileSync(stylesheet, 'utf8');
  const keys = new Set<string>();
  // CSS-module keys are class selectors. Looking only at text immediately before
  // a rule block avoids treating decimal values, URLs, or prose as class names.
  for (const match of content.matchAll(/(?:^|})\s*([^{}]+)\{/gm)) {
    for (const selector of match[1].matchAll(/\.([A-Za-z_][A-Za-z0-9_-]*)/g)) keys.add(selector[1]);
  }
  return keys;
}

function resolveStylesheet(sourceFile: string, importPath: string): string {
  const resolved = path.resolve(path.dirname(sourceFile), importPath);
  return resolved.endsWith('.css') ? resolved : `${resolved}.css`;
}

const lineOf = (content: string, index: number) => content.slice(0, index).split('\n').length;

export function scanCssModules(rootDir: string): CssModuleScan {
  const issues: CssModuleIssue[] = [];
  const unverified: CssModuleUnverified[] = [];

  for (const file of sourceFiles(rootDir)) {
    const content = fs.readFileSync(file, 'utf8');
    const imports = [...content.matchAll(/import\s+([A-Za-z_$][A-Za-z0-9_$]*)\s+from\s+['"]([^'"]+\.module\.css)['"];?/g)];
    for (const imported of imports) {
      const variable = imported[1];
      const stylesheet = resolveStylesheet(file, imported[2]);
      if (!fs.existsSync(stylesheet)) {
        issues.push({
          file, line: lineOf(content, imported.index ?? 0), stylesheet,
          kind: 'missing-stylesheet', key: '',
        });
        continue;
      }
      const keys = cssKeys(stylesheet);

      const dotted = new RegExp(`\\b${variable}\\.([A-Za-z_$][A-Za-z0-9_$]*)\\b`, 'g');
      for (const match of content.matchAll(dotted)) {
        if (keys.has(match[1])) continue;
        issues.push({ file, line: lineOf(content, match.index ?? 0), stylesheet, kind: 'missing-key', key: match[1] });
      }

      // A template key contributes its static head: `signal-${severity}` can only
      // ever produce a class starting `signal-`, so a stylesheet with no such key
      // has lost the whole family.
      const templated = new RegExp(`\\b${variable}\\[\\s*\`([^\`$]*)\\$\\{`, 'g');
      const templateIndexes = new Set<number>();
      for (const match of content.matchAll(templated)) {
        templateIndexes.add(match.index ?? 0);
        const prefix = match[1];
        if (prefix === '') continue;
        if ([...keys].some((key) => key.startsWith(prefix))) continue;
        issues.push({ file, line: lineOf(content, match.index ?? 0), stylesheet, kind: 'missing-prefix', key: prefix });
      }

      const computed = new RegExp(`\\b${variable}\\[[^\\]]*\\]`, 'g');
      for (const match of content.matchAll(computed)) {
        if (templateIndexes.has(match.index ?? 0)) continue;
        unverified.push({ file, line: lineOf(content, match.index ?? 0), expression: match[0] });
      }
    }
  }
  return { issues, unverified };
}

/** The failing subset, for callers that only decide pass or fail. */
export function findCssModuleIssues(rootDir: string): CssModuleIssue[] {
  return scanCssModules(rootDir).issues;
}

export function formatCssModuleIssues(issues: CssModuleIssue[]): string[] {
  return issues.map((issue) => {
    const file = path.relative(process.cwd(), issue.file);
    const stylesheet = path.relative(process.cwd(), issue.stylesheet);
    if (issue.kind === 'missing-stylesheet') return `${file}:${issue.line}: CSS module not found: ${stylesheet}`;
    if (issue.kind === 'missing-prefix') {
      return `${file}:${issue.line}: no CSS module key starts with ${JSON.stringify(issue.key)} in ${stylesheet}`;
    }
    return `${file}:${issue.line}: CSS module key ${JSON.stringify(issue.key)} is not defined in ${stylesheet}`;
  });
}

if (import.meta.filename === process.argv[1]) {
  const scriptDirectory = path.dirname(fileURLToPath(import.meta.url));
  const { issues, unverified } = scanCssModules(path.resolve(scriptDirectory, '../src'));
  if (issues.length > 0) {
    console.error('CSS-module consistency check failed:');
    for (const line of formatCssModuleIssues(issues)) console.error(`  ${line}`);
    process.exitCode = 1;
  } else {
    console.log('CSS-module consistency check passed.');
  }
  // Printed on pass and on fail: the size of what this check cannot see is part
  // of reading its result honestly.
  if (unverified.length > 0) {
    console.log(`  ${unverified.length} runtime-keyed access(es) not verifiable by a text scan; guard those with \`?? ''\`.`);
  }
}
