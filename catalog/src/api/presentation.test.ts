import { describe, test, expect } from 'vitest';
import { present } from './presentation';

describe('presentation', () => {
  test('Ollama presentation does not claim plan-gated models are universally callable', () => {
    const ollama = present('ollama-cloud');
    expect(ollama.blurb).not.toMatch(/every plan/i);
    expect(ollama.note ?? '').not.toMatch(/not which models/i);
    expect(ollama.note ?? '').toMatch(/excluded/i);
  });
});
