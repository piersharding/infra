import js from '@eslint/js'
import nextPlugin from '@next/eslint-plugin-next'
import reactPlugin from 'eslint-plugin-react'
import reactHooksPlugin from 'eslint-plugin-react-hooks'
import jsxA11yPlugin from 'eslint-plugin-jsx-a11y'
import jestPlugin from 'eslint-plugin-jest'

export default [
  {
    ignores: [
      '.next/**',
      'out/**',
      'node_modules/**',
      '.vercel/**',
      'coverage/**',
      '*.config.js',
      '*.config.mjs',
    ],
  },
  js.configs.recommended,
  {
    files: ['**/*.js', '**/*.jsx', '**/*.ts', '**/*.tsx'],
    plugins: {
      '@next/next': nextPlugin,
      react: reactPlugin,
      'react-hooks': reactHooksPlugin,
      'jsx-a11y': jsxA11yPlugin,
      jest: jestPlugin,
    },
    languageOptions: {
      ecmaVersion: 2021,
      sourceType: 'module',
      parserOptions: {
        ecmaFeatures: {
          jsx: true,
        },
      },
      globals: {
        // ES6+ globals
        Promise: 'readonly',
        Set: 'readonly',
        Map: 'readonly',
        // Custom globals
        jsonBody: 'readonly',
        // Browser globals
        document: 'readonly',
        window: 'readonly',
        navigator: 'readonly',
        fetch: 'readonly',
        console: 'readonly',
        URL: 'readonly',
        FileReader: 'readonly',
        setTimeout: 'readonly',
        setInterval: 'readonly',
        clearTimeout: 'readonly',
        clearInterval: 'readonly',
        requestAnimationFrame: 'readonly',
        cancelAnimationFrame: 'readonly',
        localStorage: 'readonly',
        sessionStorage: 'readonly',
        location: 'readonly',
        history: 'readonly',
        URLSearchParams: 'readonly',
        // Node.js globals
        process: 'readonly',
        global: 'readonly',
        __dirname: 'readonly',
        __filename: 'readonly',
        // Jest globals
        afterAll: 'readonly',
        afterEach: 'readonly',
        beforeAll: 'readonly',
        beforeEach: 'readonly',
        describe: 'readonly',
        expect: 'readonly',
        it: 'readonly',
        jest: 'readonly',
        test: 'readonly',
      },
    },
    settings: {
      react: {
        version: 'detect',
        runtime: 'automatic', // React 19 automatic JSX runtime
      },
    },
    rules: {
      // Next.js recommended rules
      ...nextPlugin.configs.recommended.rules,
      ...nextPlugin.configs['core-web-vitals'].rules,

      // React recommended rules
      'react/react-in-jsx-scope': 'off', // Not needed in Next.js
      'react/prop-types': 'off',
      'react/jsx-uses-react': 'off', // Not needed with React 19 automatic runtime
      'react/jsx-uses-vars': 'error', // Ensure JSX variables are detected

      // React Hooks rules
      'react-hooks/rules-of-hooks': 'error',
      'react-hooks/exhaustive-deps': 'off', // Custom override

      // Custom overrides
      '@next/next/no-img-element': 'off',

      // Relax unused vars for common patterns
      'no-unused-vars': [
        'error',
        {
          argsIgnorePattern: '^_',
          varsIgnorePattern: '^_',
          caughtErrorsIgnorePattern: '^_',
          ignoreRestSiblings: true,
        },
      ],
    },
  },
]
