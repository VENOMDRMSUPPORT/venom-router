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

/**
 * The model's ANSWER, which is `message.content` alone.
 *
 * `contentFromResponse` deliberately also folds in `message.reasoning`, because
 * the regex-graded dimensions accept a worked answer. JSON-graded dimensions
 * cannot: a reasoning model's trace is prose, so concatenating it made
 * `JSON.parse` throw on a perfect answer and scored it zero. That is what
 * silently zeroed structured output, long context and vision across a whole
 * paid evaluation run.
 */
function answerText(response: unknown): string {
  const message = messageFromResponse(response);
  return typeof message.content === 'string' ? message.content : '';
}

/** Strip one markdown code fence, which is how most models wrap JSON. */
function unfence(text: string): string {
  const trimmed = text.trim();
  if (!trimmed.startsWith('```')) return text;
  const opening = trimmed.indexOf('\n');
  if (opening < 0) return text;
  const closing = trimmed.lastIndexOf('```');
  if (closing <= opening) return text;
  return trimmed.slice(opening + 1, closing);
}

/** The first balanced JSON object in the text, so trailing prose is tolerated. */
function firstJsonObject(text: string): string | null {
  const start = text.indexOf('{');
  if (start < 0) return null;
  let depth = 0;
  let inString = false;
  let escaped = false;
  for (let index = start; index < text.length; index++) {
    const character = text[index];
    if (inString) {
      if (escaped) escaped = false;
      else if (character === '\\') escaped = true;
      else if (character === '"') inString = false;
      continue;
    }
    if (character === '"') inString = true;
    else if (character === '{') depth++;
    else if (character === '}' && --depth === 0) return text.slice(start, index + 1);
  }
  return null;
}

/**
 * The JSON object the model answered with, or `{}` when it produced none.
 *
 * Reads the answer only, unwraps a code fence, and finally falls back to the
 * first balanced object in the text. An empty object fails every criterion,
 * which is the correct verdict for a model that did not return JSON.
 */
function jsonFromResponse(response: unknown): Record<string, unknown> {
  const answer = unfence(answerText(response).trim());
  for (const candidate of [answer, firstJsonObject(answer)]) {
    if (!candidate) continue;
    try {
      const parsed = JSON.parse(candidate) as unknown;
      if (typeof parsed === 'object' && parsed !== null && !Array.isArray(parsed)) {
        return parsed as Record<string, unknown>;
      }
    } catch { /* try the next candidate */ }
  }
  return {};
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

/** Wrapping that carries no meaning: LaTeX delimiters, markdown, whitespace. */
const NOTATION_NOISE = ['$', '{', '}', '(', ')', '[', ']', '*', '`', ' ', '\t', '\n', '\r', String.fromCharCode(92)];

/**
 * Whether the response states the relation the prompt asked for.
 *
 * Accepts the two shapes a correct answer actually takes: the forward equation
 * (`7x + 5 = 26`, however it is spaced and whether or not it arrives wrapped in
 * LaTeX or markdown), or the rearranged division (`(26 - 5) / 7`). Notation is
 * not the thing under test — solving is.
 */
function solvesTheEquation(text: string, multiplier: number, added: number, result: number): boolean {
  const flat = NOTATION_NOISE.reduce((value, character) => value.split(character).join(''), text);
  const forward = new RegExp(`${multiplier}[a-z*·×]*[+]?${added}=${result}`, 'i');
  const rearranged = new RegExp(`${result}-${added}[/÷]${multiplier}`, 'i');
  return forward.test(flat) || rearranged.test(flat);
}

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
        // Did it produce the EQUATION the prompt asked for — not a division,
        // which the prompt never mentions.
        //
        // The old check was `includes('/')`. A model answering
        // "7x + 5 = 26  =>  x = 3" solves it perfectly and writes no division,
        // so a correct answer scored 4 of 5. It passed anyway only because the
        // grader also reads the reasoning trace, which nearly always contains a
        // slash somewhere. Replaying 1,200 retained samples showed what that was
        // covering: graded on their answers alone, eleven of twenty identities
        // dropped, one by 93 points — every one of them for notation rather than
        // for being wrong.
        solvesTheEquation(out, multiplier, added, result),
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
      const parsed = jsonFromResponse(response);
      return grade([
        parsed.first === first,
        parsed.second === second,
        parsed.combined === `${first}:${second}`,
        Object.keys(parsed).length === 3,
        // The echo check reads the ANSWER only: a reasoning trace may legitimately
        // quote the filler while working, and that is not the model answering with it.
        !answerText(response).includes('routine inventory'),
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
      const parsed = jsonFromResponse(response);
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
      const parsed = jsonFromResponse(response);
      // The prompt asks for the digit, never for a particular JSON type, so a
      // model that reads the image correctly and writes 1 rather than "1" has
      // answered. Requiring the string form failed a right answer on two of the
      // five criteria. The shape check keeps five real criteria.
      const seen = parsed.digit === undefined || parsed.digit === null ? '' : String(parsed.digit);
      return grade([
        seen === digit,
        parsed.color === 'black',
        parsed.background === 'white',
        Object.keys(parsed).length === 3,
        /^[0-9]$/.test(seen),
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
