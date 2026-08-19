import { createHash } from 'node:crypto';
import type { RuntimeScenario } from './runtime.ts';
import type { QualityDimension } from './score.ts';

export interface EvaluationFixture extends RuntimeScenario {
  expectedResponse: unknown;
}

type Grade = { weightedSuccesses: number; weightedCriteria: number };

function messageFromResponse(response: unknown): Record<string, unknown> {
  if (typeof response !== 'object' || response === null) return {};
  const choices = (response as Record<string, unknown>).choices;
  if (!Array.isArray(choices) || typeof choices[0] !== 'object' || choices[0] === null) return {};
  const message = (choices[0] as Record<string, unknown>).message;
  return typeof message === 'object' && message !== null ? message as Record<string, unknown> : {};
}

function contentFromResponse(response: unknown): string {
  const message = messageFromResponse(response);
  const content = typeof message.content === 'string' ? message.content : '';
  const reasoning = typeof message.reasoning === 'string' ? message.reasoning : '';
  return `${content}\n${reasoning}`.trim();
}

const grade = (checks: boolean[]): Grade => ({
  weightedSuccesses: checks.filter(Boolean).length,
  weightedCriteria: 5,
});

function fixture(
  id: string,
  messages: unknown[],
  expectedResponse: unknown,
  evaluator: (response: unknown) => Grade,
  extra: Record<string, unknown> = {},
): EvaluationFixture {
  return {
    id,
    payload: { messages, temperature: 0, max_tokens: 512, ...extra },
    expectedResponse,
    grade: evaluator,
  };
}

const coding = Array.from({ length: 20 }, (_, index) => {
  const n = index + 2;
  const name = `isDivisible${n}`;
  const expected = `function ${name}(value) { return Number.isInteger(value) && value % ${n} === 0; }\n${name}(${n * 3}); // true`;
  return fixture(
    `coding-${String(index + 1).padStart(2, '0')}`,
    [{ role: 'user', content: `Write JavaScript function ${name}(value). It must return true only for integer values divisible by ${n}. Include an example call with ${n * 3}. Return code only.` }],
    { choices: [{ message: { content: expected } }] },
    (response) => {
      const out = contentFromResponse(response);
      return grade([
        new RegExp(`function\\s+${name}\\s*\\(`).test(out),
        out.includes(`% ${n}`) || out.includes(`%${n}`),
        /Number\.isInteger\s*\(/.test(out),
        /===\s*0/.test(out),
        out.includes(`${name}(${n * 3})`),
      ]);
    },
  );
});

const reasoning = Array.from({ length: 20 }, (_, index) => {
  const multiplier = index + 7;
  const original = index + 3;
  const added = index + 5;
  const result = multiplier * original + added;
  const expected = `(${result} - ${added}) / ${multiplier} = ${original}`;
  return fixture(
    `reasoning-${String(index + 1).padStart(2, '0')}`,
    [{ role: 'user', content: `A number is multiplied by ${multiplier}, then ${added} is added, producing ${result}. Return one equation that solves for the original number and the numeric answer.` }],
    { choices: [{ message: { content: expected } }] },
    (response) => {
      const out = contentFromResponse(response);
      return grade([
        new RegExp(`(^|\\D)${original}(\\D|$)`).test(out),
        out.includes(String(result)),
        out.includes(String(added)),
        out.includes(String(multiplier)),
        out.includes('/') || out.includes('÷'),
      ]);
    },
  );
});

const longContext = Array.from({ length: 20 }, (_, index) => {
  const first = `alpha-${String(index + 1).padStart(2, '0')}`;
  const second = `omega-${String(20 - index).padStart(2, '0')}`;
  const filler = Array.from({ length: 100 }, (_, part) => `Record ${part + 1}: routine inventory entry with no authorization.`).join(' ');
  const expected = JSON.stringify({ first, second, combined: `${first}:${second}` });
  return fixture(
    `long-context-${String(index + 1).padStart(2, '0')}`,
    [{ role: 'user', content: `Opening authorization token: ${first}. ${filler} Closing authorization token: ${second}. ${filler} Return JSON only with first, second, and combined where combined is first:second.` }],
    { choices: [{ message: { content: expected } }] },
    (response) => {
      const out = contentFromResponse(response);
      let parsed: Record<string, unknown> = {};
      try { parsed = JSON.parse(out) as Record<string, unknown>; } catch { /* model failure */ }
      return grade([
        parsed.first === first,
        parsed.second === second,
        parsed.combined === `${first}:${second}`,
        Object.keys(parsed).length === 3,
        !out.includes('routine inventory'),
      ]);
    },
  );
});

const toolCalling = Array.from({ length: 20 }, (_, index) => {
  const city = ['Cairo', 'Alexandria', 'Giza', 'Luxor'][index % 4];
  const call = { id: `call-${index + 1}`, type: 'function', function: { name: 'get_weather', arguments: JSON.stringify({ city }) } };
  return fixture(
    `tool-calling-${String(index + 1).padStart(2, '0')}`,
    [{ role: 'user', content: `Use get_weather exactly once for ${city}. Do not answer from memory.` }],
    { choices: [{ message: { content: null, tool_calls: [call] } }] },
    (response) => {
      const calls = messageFromResponse(response).tool_calls;
      const first = Array.isArray(calls) && typeof calls[0] === 'object' && calls[0] !== null
        ? calls[0] as Record<string, unknown> : {};
      const fn = typeof first.function === 'object' && first.function !== null
        ? first.function as Record<string, unknown> : {};
      let args: Record<string, unknown> = {};
      try { args = JSON.parse(String(fn.arguments ?? '{}')) as Record<string, unknown>; } catch { /* model failure */ }
      return grade([
        Array.isArray(calls),
        Array.isArray(calls) && calls.length === 1,
        fn.name === 'get_weather',
        args.city === city,
        Object.keys(args).length === 1,
      ]);
    },
    {
      tools: [{ type: 'function', function: { name: 'get_weather', description: 'Get current weather for a city.', parameters: { type: 'object', properties: { city: { type: 'string' } }, required: ['city'], additionalProperties: false } } }],
    },
  );
});

const structuredOutput = Array.from({ length: 20 }, (_, index) => {
  const id = index + 1;
  const label = `catalog-${id}`;
  const expected = JSON.stringify({ recordId: id, approved: true, label });
  return fixture(
    `structured-output-${String(id).padStart(2, '0')}`,
    [{ role: 'user', content: `Return JSON only for record ${id} with integer recordId ${id}, boolean approved true, and string label ${label}. No extra fields.` }],
    { choices: [{ message: { content: expected } }] },
    (response) => {
      let parsed: Record<string, unknown> = {};
      try { parsed = JSON.parse(contentFromResponse(response)) as Record<string, unknown>; } catch { /* model failure */ }
      return grade([
        Number.isInteger(parsed.recordId),
        parsed.recordId === id,
        parsed.approved === true,
        parsed.label === label,
        Object.keys(parsed).length === 3,
      ]);
    },
  );
});

const vision = Array.from({ length: 20 }, (_, index) => {
  const digit = String((index + 1) % 10);
  const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="128" height="128"><rect width="128" height="128" fill="white"/><text x="64" y="94" font-size="96" text-anchor="middle" fill="black">${digit}</text></svg>`;
  const image = `data:image/svg+xml;base64,${Buffer.from(svg).toString('base64')}`;
  const expected = JSON.stringify({ digit, color: 'black', background: 'white' });
  return fixture(
    `vision-${String(index + 1).padStart(2, '0')}`,
    [{ role: 'user', content: [{ type: 'text', text: 'Return JSON only with digit, color, and background for this image.' }, { type: 'image_url', image_url: { url: image } }] }],
    { choices: [{ message: { content: expected } }] },
    (response) => {
      let parsed: Record<string, unknown> = {};
      try { parsed = JSON.parse(contentFromResponse(response)) as Record<string, unknown>; } catch { /* model failure */ }
      return grade([
        parsed.digit === digit,
        parsed.color === 'black',
        parsed.background === 'white',
        Object.keys(parsed).length === 3,
        typeof parsed.digit === 'string',
      ]);
    },
  );
});

export function buildEvaluationFixtures(): Record<QualityDimension, EvaluationFixture[]> {
  return { coding, reasoning, longContext, toolCalling, structuredOutput, vision };
}

export function fixtureDigest(fixtures: Record<QualityDimension, EvaluationFixture[]>): string {
  const serializable = Object.fromEntries(Object.entries(fixtures).map(([dimension, scenarios]) => [dimension,
    scenarios.map(({ id, payload, expectedResponse }) => ({ id, payload, expectedResponse })),
  ]));
  return createHash('sha256').update(JSON.stringify(serializable)).digest('hex');
}
