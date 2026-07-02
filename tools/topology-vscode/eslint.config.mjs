// Flat ESLint config for the webview/extension TypeScript.
// Medium concern (lint tooling for a React codebase): adopt the dominant
// stack — typescript-eslint + eslint-plugin-react-hooks. Ruleset is kept
// practical (correctness/hooks, not stylistic) to avoid a noise diff.
import js from '@eslint/js';
import tseslint from 'typescript-eslint';
import reactHooks from 'eslint-plugin-react-hooks';

export default tseslint.config(
  {
    ignores: ['out/**', 'node_modules/**', 'test/**', '**/*.js', '**/*.mjs'],
  },
  {
    files: ['src/**/*.{ts,tsx}'],
    // Honor the pre-existing deliberate `eslint-disable` comments as-is: some no
    // longer flag under the current plugin versions, but they encode intent and
    // must not be nagged about (task: honor existing disables, don't remove).
    linterOptions: { reportUnusedDisableDirectives: 'off' },
    // Type-checked ruleset: catches the any-leak / floating-promise / misused-
    // promise / bad-stringification correctness bug classes that the syntactic
    // ruleset can't see. It needs the full type graph (projectService builds it
    // from tsconfig), so it is slower — that cost is gated in stop-checks' ts-
    // gated expensive block (check-eslint), not the fast path.
    extends: [js.configs.recommended, ...tseslint.configs.recommendedTypeChecked],
    languageOptions: {
      parserOptions: { projectService: true, tsconfigRootDir: import.meta.dirname },
    },
    plugins: {
      'react-hooks': reactHooks,
    },
    rules: {
      ...reactHooks.configs.recommended.rules,
      // Both hook rules are error — this is the correctness bug class the
      // guard exists to catch. Pre-existing deliberate disables (grep
      // `react-hooks/exhaustive-deps`) stay honored and suppress their sites,
      // so the codebase lints clean; only NEW violations fail check-eslint.
      'react-hooks/rules-of-hooks': 'error',
      'react-hooks/exhaustive-deps': 'error',
      // Practical relaxations — these fire broadly on existing code and are
      // style/preference, not correctness. Keep the diff focused on hook bugs
      // rather than a codebase-wide restyle.
      '@typescript-eslint/no-explicit-any': 'off',
      '@typescript-eslint/no-unused-vars': 'off',
    },
  },
);
