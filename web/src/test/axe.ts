import axeCore from 'axe-core';
import { expect } from 'vitest';

/**
 * Runs a real axe-core accessibility scan against container and asserts
 * zero violations — V7-03's own "automated accessibility smoke" Verify
 * line. This is a plain function, not a custom Vitest matcher: the
 * `vitest-axe` package's own matcher type augmentation does not line up
 * with this project's pinned Vitest 5's actual `Assertion<T, R>` shape
 * (its `interface Assertion<T = any>` has one type parameter, Vitest 5's
 * has two — TypeScript interface merging silently fails to apply, leaving
 * `toHaveNoViolations` unrecognized), so this avoids depending on a
 * cross-package type-augmentation contract that already proved fragile.
 *
 * jsdom has no real layout/paint engine (no HTMLCanvasElement#getContext),
 * so `color-contrast` is disabled — it can never compute a real answer
 * here, only ever warn or produce a meaningless pass. Every other rule
 * (labels, roles, aria-*, focus order, etc.) still runs for real.
 */
export async function expectNoAxeViolations(container: Element): Promise<void> {
  const results = await axeCore.run(container, {
    rules: { 'color-contrast': { enabled: false } },
  });
  if (results.violations.length > 0) {
    const summary = results.violations
      .map((v) => `- [${v.impact}] ${v.id}: ${v.help} (${v.nodes.length} node(s))\n  ${v.helpUrl}`)
      .join('\n');
    expect.fail(`axe found ${results.violations.length} accessibility violation(s):\n${summary}`);
  }
}
