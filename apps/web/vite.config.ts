/// <reference types="vitest/config" />
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// Dev: proxy the edge API so the browser stays same-origin (as on the Pi).
const edge = process.env.AEROLAB_EDGE ?? 'http://127.0.0.1:18080'
export default defineConfig({
  plugins: [react()],
  server: { proxy: { '/api': edge, '/healthz': edge, '/readyz': edge } },
  build: { outDir: 'dist', sourcemap: false },
  test: { environment: 'jsdom', setupFiles: ['./src/test-setup.ts'], globals: true },
})
