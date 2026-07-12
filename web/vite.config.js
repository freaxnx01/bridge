import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'

export default defineConfig({
  plugins: [svelte()],
  resolve: {
    // Under Vitest, Svelte 5's package `exports` map resolves to the server
    // (SSR) build unless the `browser` condition is forced, so components
    // fail to mount in jsdom with "lifecycle_function_unavailable". `[]`
    // (Vite's default conditions) is used everywhere else, including builds.
    conditions: process.env.VITEST ? ['browser'] : [],
  },
  build: {
    // Output goes to internal/web/dist so go:embed picks it up.
    // Vite will NOT empty the outDir because it's outside the Vite root (web/).
    outDir: '../internal/web/dist',
  },
  server: {
    proxy: {
      '/api': 'http://localhost:7777',
    },
  },
  test: {
    environment: 'jsdom',
    globals: true,
  },
})
