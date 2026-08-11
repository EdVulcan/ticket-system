import { resolve } from 'path'
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  root: resolve('src/renderer'),
  envDir: resolve('.'),
  base: './',
  resolve: {
    alias: {
      '@renderer': resolve('src/renderer/src')
    }
  },
  plugins: [vue()],
  server: process.env.VITE_DEV_PROXY_TARGET ? {
    proxy: {
      '/api': {
        target: process.env.VITE_DEV_PROXY_TARGET,
        changeOrigin: true,
        secure: true
      }
    }
  } : undefined,
  build: {
    outDir: resolve('dist'),
    emptyOutDir: true
  }
})
