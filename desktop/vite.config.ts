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
  build: {
    outDir: resolve('dist'),
    emptyOutDir: true
  }
})
