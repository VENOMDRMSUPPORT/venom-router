/**
 * SPA test runner.
 *
 * Separate from the backend's `node --test` because the two need different
 * environments — jsdom here, none there — but both are wired into `npm test`, so
 * a UI regression fails the same command a resolver regression does.
 *
 * Known limitation recorded for this repo: jsdom implements no `AnimationEvent`,
 * so React never wires `onAnimationStart`/`onAnimationEnd` under it. Anything
 * built on those must be tested as pure decision logic, not through the DOM.
 */
import { defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';

export default defineConfig({
  plugins: [react()],
  test: {
    environment: 'jsdom',
    globals: true,
    include: ['src/**/*.test.{ts,tsx}', 'docs-site/src/**/*.test.{ts,tsx}'],
    setupFiles: ['src/test-setup.ts'],
    css: true,
  },
});
