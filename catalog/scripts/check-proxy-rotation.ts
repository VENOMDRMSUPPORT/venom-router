#!/usr/bin/env node
import { createHash } from 'node:crypto';
import { isIP } from 'node:net';
import {
  fetchForEvaluationProvider,
  resolveEvaluationProxyListUrl,
} from '../sync/evaluation/proxy-pool.ts';

export interface ProxyCheckResult {
  samples: number;
  successful: number;
  uniqueExits: number;
  allDifferent: boolean;
}

export interface ProxyCheckInput {
  providerId: string;
  samples: number;
  fetchImpl?: (input: string | URL, init?: RequestInit) => Promise<Response>;
  write?: (line: string) => void;
}

function maskIp(ip: string): string {
  if (isIP(ip) === 4) {
    const parts = ip.split('.');
    return `${parts[0]}.${parts[1]}.x.x`;
  }
  if (isIP(ip) === 6) return `${ip.split(':').slice(0, 2).join(':')}:x:x`;
  return 'invalid';
}

function fingerprint(value: string): string {
  return createHash('sha256').update(value).digest('hex').slice(0, 12);
}

export async function runProxyCheck(input: ProxyCheckInput): Promise<ProxyCheckResult> {
  const write = input.write ?? console.log;
  const providerFetch = input.fetchImpl ?? fetchForEvaluationProvider(input.providerId);
  const exits = new Set<string>();
  let successful = 0;

  for (let attempt = 1; attempt <= input.samples; attempt++) {
    try {
      const response = await providerFetch('https://api.ipify.org', {
        signal: AbortSignal.timeout(30_000),
      });
      const ip = (await response.text()).trim();
      if (!response.ok || isIP(ip) === 0) throw new Error('invalid exit response');
      successful++;
      exits.add(ip);
      write(`${attempt}/${input.samples}  ${maskIp(ip)}  ${fingerprint(ip)}`);
    } catch {
      write(`${attempt}/${input.samples}  failed`);
    }
  }

  const result = {
    samples: input.samples,
    successful,
    uniqueExits: exits.size,
    allDifferent: successful === input.samples && exits.size === input.samples,
  };
  write(`successful ${result.successful}/${result.samples}; unique exits ${result.uniqueExits}; all different ${result.allDifferent}`);
  return result;
}

function argument(name: string): string | null {
  const prefix = `--${name}=`;
  return process.argv.find((value) => value.startsWith(prefix))?.slice(prefix.length) ?? null;
}

if (import.meta.filename === process.argv[1]) {
  const providerId = process.argv[2];
  const samples = Number(argument('samples') ?? 5);
  if (!providerId || !Number.isInteger(samples) || samples < 2 || samples > 20) {
    console.error('usage: npm run proxy:check -- <providerId> [--samples=2..20]');
    process.exit(1);
  }
  if (!resolveEvaluationProxyListUrl(providerId)) {
    console.error(`proxy rotation is not configured for ${providerId}`);
    process.exit(1);
  }
  const result = await runProxyCheck({ providerId, samples });
  if (!result.allDifferent) process.exitCode = 1;
}
