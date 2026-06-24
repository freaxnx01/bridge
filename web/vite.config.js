import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'

export default defineConfig({
  plugins: [svelte()],
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
