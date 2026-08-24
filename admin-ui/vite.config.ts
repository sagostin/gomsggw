import vue from '@vitejs/plugin-vue'
import tailwindcss from '@tailwindcss/vite'
import { defineConfig } from 'vite'

// Paths the gateway admin API serves. In dev these are proxied to the
// gateway so the SPA and API share an origin (no CORS needed).
const API_PREFIXES = ['/clients', '/carriers', '/numbers', '/stats']

const gatewayTarget = process.env.GATEWAY_URL || 'http://localhost:3000'

export default defineConfig({
  // Served under /ui/ so SPA routes never collide with the gateway API
  // paths (/clients, /carriers, ...) that the proxy intercepts.
  base: '/ui/',
  plugins: [vue(), tailwindcss()],
  server: {
    proxy: Object.fromEntries(
      API_PREFIXES.map((p) => [p, { target: gatewayTarget, changeOrigin: true }]),
    ),
  },
})
