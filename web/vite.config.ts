import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

export default defineConfig({
  plugins: [svelte()],
  server: {
    proxy: { '/api': 'http://localhost:8080' },
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
    manifest: true, // scripts/budget.mjs reads the entry's static imports from it
  },
});
