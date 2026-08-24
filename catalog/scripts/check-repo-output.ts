import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

/** Runtime logs belong outside the checkout; no project log directory is allowlisted. */
export const ALLOWED_LOG_DIRECTORIES: readonly string[] = [];

export function findRepositoryLogs(rootDir: string): string[] {
  const logs: string[] = [];
  const visit = (directory: string) => {
    for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
      if (entry.name === '.git' || entry.name === 'node_modules' || entry.name === 'dist') continue;
      const absolute = path.join(directory, entry.name);
      if (entry.isDirectory()) {
        visit(absolute);
      } else if (entry.isFile() && entry.name.toLowerCase().endsWith('.log')) {
        const relative = path.relative(rootDir, absolute).replaceAll(path.sep, '/');
        if (!ALLOWED_LOG_DIRECTORIES.some((directory) => relative.startsWith(`${directory}/`))) logs.push(relative);
      }
    }
  };
  visit(rootDir);
  return logs.sort();
}

if (import.meta.filename === process.argv[1]) {
  const scriptDirectory = path.dirname(fileURLToPath(import.meta.url));
  const projectRoot = path.resolve(scriptDirectory, '..');
  const logs = findRepositoryLogs(projectRoot);
  if (logs.length > 0) {
    console.error('Repository output check failed. Remove generated log files or redirect output outside the checkout:');
    for (const log of logs) console.error(`  ${log}`);
    process.exitCode = 1;
  } else {
    console.log('Repository output check passed.');
  }
}
