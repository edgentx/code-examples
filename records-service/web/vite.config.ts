import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// The console is served from the same origin as the API in a deployment, so the
// development server proxies /api rather than the browser making a cross-origin
// request. Nothing here needs a wildcard CORS policy, in development or in
// production.
const API = 'http://127.0.0.1:8081';

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: { '/api': { target: API, changeOrigin: false } },
  },
  build: {
    outDir: 'dist',
    sourcemap: true,
  },
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./tests/setup.ts'],
    include: ['src/**/*.test.ts', 'src/**/*.test.tsx'],
  },
});
