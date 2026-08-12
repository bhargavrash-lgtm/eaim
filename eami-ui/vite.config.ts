import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import path from 'path'

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  server: {
    port: 5173,
    // Native FS-event watching (chokidar's default) does not reliably see
    // writes made from the Windows host into this container's bind-mounted
    // ./eami-ui/src (docker-compose.yml) -- Docker Desktop's host<->Linux
    // filesystem bridge on Windows doesn't propagate inotify events across
    // that boundary. Without polling, the dev server keeps serving its
    // in-memory module cache indefinitely after an edit -- confirmed live:
    // a real source change was on disk (and inside the container's bind
    // mount, byte-for-byte) but the module actually served over HTTP was
    // stale until the process was restarted. usePolling forces Vite to
    // periodically re-stat watched files instead of relying on FS events,
    // closing the gap regardless of host OS/filesystem-bridge behavior.
    watch: {
      usePolling: true,
      interval: 300,
    },
    proxy: {
      '/v1': {
        target: process.env.VITE_API_URL ?? 'http://localhost:8080',
        changeOrigin: true,
      },
    },
  },
})
