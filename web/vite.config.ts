/// <reference types="vitest/config" />
import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

// The bundle lands in the Go package that embeds it (internal/server, `all:dist`),
// so `make web && make install` ships one binary. emptyOutDir wipes the .gitkeep
// there - `make web` puts it back after the build.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: {
    outDir: '../internal/server/dist',
    emptyOutDir: true,
  },
  server: {
    // `pm serve` on its default address; the dev server only adds HMR.
    // PM_API_URL points the proxy at another pm serve (a worktree slot's rig
    // runs its own on the slot's PM_API_PORT) without editing this file.
    proxy: {
      '/api': process.env.PM_API_URL ?? 'http://127.0.0.1:7070',
    },
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['src/test/setup.ts'],
  },
})
