import { defineConfig } from 'tsup';

export default defineConfig({
  entry: { main: 'src/cli/main.ts' },
  format: ['cjs'],
  platform: 'node',
  target: 'node23',
  outDir: 'dist',
  splitting: false,
  clean: true,
  dts: false,
  loader: {
    ".sh": "text",
  },
});
