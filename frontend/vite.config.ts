import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// The portal only talks to the API gateway. In development Vite proxies /api
// to the gateway's internal listener so the browser stays same-origin.
const gateway = process.env.GATEWAY_URL ?? 'http://127.0.0.1:8080';

export default defineConfig({
  plugins: [react()],
  server: {
    host: '127.0.0.1',
    port: 5173,
    proxy: { '/api': { target: gateway, changeOrigin: true } },
  },
  preview: {
    host: '127.0.0.1',
    port: 4173,
    proxy: { '/api': { target: gateway, changeOrigin: true } },
  },
});
