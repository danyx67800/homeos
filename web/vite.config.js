import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import tailwindcss from '@tailwindcss/vite';

// The build output is copied to /opt/homeos/web, which phase 1's Caddyfile
// serves with `try_files {path} /index.html` — so client-side routing survives
// a hard refresh and there is no server-side route table to keep in step.
export default defineConfig({
  plugins: [svelte(), tailwindcss()],

  build: {
    outDir: 'dist',
    emptyOutDir: true,
    // The dashboard is served from a box on the LAN, often a Raspberry Pi with
    // a slow SD card. Inlining small assets and keeping one CSS file cuts the
    // request count on first paint.
    assetsInlineLimit: 8192,
    chunkSizeWarningLimit: 600,
    rollupOptions: {
      output: {
        // Content hashes let Caddy cache aggressively while an OTA update
        // still invalidates everything that changed.
        entryFileNames: 'assets/[name].[hash].js',
        chunkFileNames: 'assets/[name].[hash].js',
        assetFileNames: 'assets/[name].[hash][extname]',
      },
    },
  },

  server: {
    port: 5173,
    strictPort: true,
    // In development the dashboard runs on Vite and the daemon on 8790, so the
    // same-origin assumption the production build relies on has to be faked.
    proxy: {
      '/api': { target: 'http://127.0.0.1:8790', changeOrigin: true },
      '/events': { target: 'http://127.0.0.1:8790', changeOrigin: true },
      '/ws': { target: 'ws://127.0.0.1:8790', ws: true },
    },
  },
});
