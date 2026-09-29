// SPDX-License-Identifier: Apache-2.0
import js from '@eslint/js';
import reactHooks from 'eslint-plugin-react-hooks';
import tseslint from 'typescript-eslint';

export default tseslint.config(
  js.configs.recommended,
  ...tseslint.configs.recommended,
  reactHooks.configs['recommended-latest'],
  {
    ignores: ['dist/**'],
    rules: {
      // Three.js scenes legitimately use non-null assertions and empty
      // loops for animation scaffolding; keep the signal high instead.
      '@typescript-eslint/no-non-null-assertion': 'off',
      '@typescript-eslint/no-empty-function': 'off',
    },
  },
);
