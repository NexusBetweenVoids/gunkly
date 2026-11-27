import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

export default defineConfig({
  // Relative: one bundle serves any GUNKLY_BASE_URL path prefix (the server rewrites index.html).
  base: './',
  plugins: [svelte({ compilerOptions: { runes: true } })],
  server: {
    proxy: { '/api': 'http://localhost:4865' },
  },
  build: {
    outDir: 'dist',
    // dist/.gitkeep is recreated by the build script so `go build` works pre-build.
    emptyOutDir: true,
    assetsDir: 'assets',
    // CSP is script-src 'self': never inline anything.
    assetsInlineLimit: 0,
    modulePreload: { polyfill: false },
    cssCodeSplit: false,
    sourcemap: false,
    rolldownOptions: {
      output: {
        // Fewer, larger chunks gzip far better than many tiny ones (the budget sums per-file gzip sizes):
        // the Svelte runtime and entry-time UI stay separate; each screen group ships with its message file.
        advancedChunks: {
          groups: [
            { name: 'svelte', test: /node_modules\/svelte/, priority: 9 },
            { name: 'shared', test: /src\/(components\/(Alert|Button|Field|Toast|Logo|Lazy|Initials|SecondFactor|LegalLinks)\.svelte|lib\/(?!rrule|zones|pow|qr|markdown|i18n\/en\/(?!core)))/, priority: 8 },
            { name: 'ui', test: /src\/components\/(Dialog|Select|Confirm|TimeZone|RecoveryCodes)\.svelte/, priority: 7 },
            { name: 'calendar', test: /src\/(screens\/Calendar|lib\/i18n\/en\/calendar)/, priority: 5 },
            { name: 'auth', test: /src\/(screens\/(Setup|Signup|ForgotPassword|ResetPassword|VerifyEmail|Invite)|components\/StepUp|lib\/i18n\/en\/auth)/, priority: 5 },
            { name: 'channels', test: /src\/(components\/(Channels|Deliveries)|lib\/i18n\/en\/channels)/, priority: 5 },
            { name: 'chores', test: /src\/(screens\/Chore|components\/(RuleBuilder|ReminderEditor|DateDialog|AiSuggest)|lib\/(rrule|i18n\/en\/chores))/, priority: 5 },
            { name: 'household', test: /src\/(screens\/(Household|NewHousehold|household\/)|lib\/i18n\/en\/household)/, priority: 5 },
            { name: 'account', test: /src\/(screens\/(Account|account\/)|components\/QrCode|lib\/qr|lib\/i18n\/en\/account)/, priority: 5 },
            { name: 'admin', test: /src\/(screens\/(Admin|admin\/)|components\/Markdown|lib\/(markdown|i18n\/en\/admin))/, priority: 5 },
          ],
        },
      },
    },
    manifest: true, // scripts/budget.mjs reads the entry's static imports from it
  },
});
