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

// jsdom has never implemented window.matchMedia (it has no real layout
// engine to evaluate a media query against) — App.tsx's own responsive
// nav-collapse effect calls it unconditionally on mount, so any test that
// renders App throws without this. A minimal stand-in that always reports
// "does not match" is correct for jsdom's own fixed, layout-less viewport.
if (typeof window !== 'undefined' && !window.matchMedia) {
  window.matchMedia = (query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  }) as MediaQueryList;
}
