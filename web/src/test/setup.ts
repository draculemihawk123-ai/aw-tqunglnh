import '@testing-library/jest-dom/vitest';
import { afterEach } from 'vitest';
import { cleanup } from '@testing-library/react';

// @testing-library/react's own auto-cleanup relies on detecting afterEach
// as an AMBIENT global, which this project deliberately does not enable
// (vite.config.ts's own `test.globals: false` — explicit imports
// everywhere else in this codebase). Without this, JSDOM's document
// accumulates every test's rendered output across the whole file, and a
// later getByRole/getByText call can match nodes left over from an
// earlier test.
afterEach(() => {
  cleanup();
});
