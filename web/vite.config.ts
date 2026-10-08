/// <reference types="vitest/config" />
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  server: {
    port: 5173,
    // The server refuses requests whose Origin is not its own Host (see guard in internal/server), so the
    // dev proxy presents itself as the backend; otherwise every POST from the dev page is "forbidden origin".
    proxy: { '/api': { target: 'http://127.0.0.1:7420', changeOrigin: true, headers: { Origin: 'http://127.0.0.1:7420' } } },
  },
  test: { environment: 'jsdom' },
})
