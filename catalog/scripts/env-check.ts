#!/usr/bin/env node
/**
 * What credentials this process can actually see — before anything is clicked.
 *
 * `missing_credentials` used to be discoverable in exactly one way: open the
 * catalog, pick a model, press Evaluate, and read a sentence that named no
 * variable. Two different causes produced that identical sentence — an env file
 * nothing loaded, and a variable whose NAME was corrupted by a BOM — so fixing
 * one left the other looking like the same unchanged bug.
 *
 * This answers it in one command, names every variable, and distinguishes the
 * two causes. Presence only: a credential value is never read or printed.
 *
 *   npm run env:check
 *   npm run env:check -- --strict   # exit 1 if any name is corrupted
 *
 * Run it through npm, not `node scripts/env-check.ts`: the npm script is what
 * passes `--env-file-if-exists=.env`, so running it bare reports the shell's
 * environment instead of the one the service gets.
 */
import { evaluationCredentialReport, type CredentialStatus } from '../sync/evaluation/provider-transport.ts';
import { proxyListEnvName, resolveEvaluationProxyListUrl } from '../sync/evaluation/proxy-pool.ts';

const MARK: Record<CredentialStatus['state'], string> = {
  present: 'ok  ',
  missing: 'MISS',
  malformed_name: 'BAD ',
};

export function renderEnvReport(report: CredentialStatus[]): string[] {
  const width = Math.max(...report.map((row) => row.providerId.length));
  const lines = [
    'catalog environment — credentials this process can see',
    '(names only; a credential value is never read or printed)',
    '',
  ];
  for (const row of report) {
    const head = `  ${MARK[row.state]}  ${row.providerId.padEnd(width)}  ${row.envName}`;
    if (row.state === 'present') lines.push(head);
    else if (row.state === 'missing') lines.push(`${head}  — not set in this process`);
    else {
      lines.push(`${head}  — SET UNDER A CORRUPTED NAME ${JSON.stringify(row.foundAs)}`);
      lines.push('        A UTF-8 BOM at the head of catalog/.env binds to the FIRST variable name and');
      lines.push('        node --env-file does not strip it. Re-save the file as UTF-8 without a BOM.');
    }
  }

  const optional = report
    .map((row) => ({ providerId: row.providerId, envName: proxyListEnvName(row.providerId) }))
    .filter((row): row is { providerId: string; envName: string } => row.envName !== null);
  if (optional.length > 0) {
    lines.push('', '  optional — proxy rotation');
    for (const row of optional) {
      const set = resolveEvaluationProxyListUrl(row.providerId) !== null;
      lines.push(`  ${set ? 'ok  ' : '—   '}  ${row.providerId}  ${row.envName}${set ? '' : '  (rotation off)'}`);
    }
  }

  const present = report.filter((row) => row.state === 'present').length;
  const corrupted = report.filter((row) => row.state === 'malformed_name').length;
  lines.push('', `${present} of ${report.length} evaluation credentials are readable.`);
  if (corrupted > 0) {
    lines.push(`${corrupted} are present in the environment but filed under a name nothing asks for.`);
  }
  if (present < report.length) {
    lines.push('Set what is missing in catalog/.env (see catalog/.env.example), then restart the service.');
  }
  return lines;
}

if (import.meta.filename === process.argv[1]) {
  const report = evaluationCredentialReport();
  for (const line of renderEnvReport(report)) console.log(line);
  if (process.argv.includes('--strict') && report.some((row) => row.state === 'malformed_name')) {
    // A missing key can be a deliberate choice. A corrupted NAME never is.
    process.exitCode = 1;
  }
}
