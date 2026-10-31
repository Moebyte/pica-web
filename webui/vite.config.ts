import { readFileSync } from 'node:fs'
import { fileURLToPath, URL } from 'node:url'
import { defineConfig, type Plugin } from 'vite'
import vue from '@vitejs/plugin-vue'
import { mockApiPlugin } from './mock/plugin'

const pkg = JSON.parse(
  readFileSync(fileURLToPath(new URL('./package.json', import.meta.url)), 'utf-8'),
) as { version: string }

export default defineConfig({
  base: '/v2/',
  plugins: [vue(), mockApiPlugin() as Plugin, versionPlugin(pkg.version)],
  define: {
    __APP_VERSION__: JSON.stringify(pkg.version),
  },
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: {
    port: 5180,
    strictPort: true,
    proxy: {
      '/api': 'http://127.0.0.1:18081',
      // /healthz lives at the gateway root (same-origin in production; proxied in dev)
      '/healthz': 'http://127.0.0.1:18081',
    },
  },
  build: {
    target: 'es2022',
    reportCompressedSize: false,
  },
})

function versionPlugin(version: string): Plugin {
  return {
    name: 'define-app-version',
    config() {
      return { define: { __APP_VERSION__: JSON.stringify(version) } }
    },
  }
}
