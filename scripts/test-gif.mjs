import { writeFile } from "node:fs/promises";
import { encodeGIF } from "../internal/app/web/gif-worker.v1.js";
const frames = [];
for (let frame = 0; frame < 16; frame++) {
  const pixels = new Uint8ClampedArray(256 * 256 * 4);
  for (let y = 30; y < 90; y++)
    for (let x = 10 + frame * 8; x < 50 + frame * 8; x++) {
      const i = (y * 256 + x) * 4;
      pixels[i] = 220;
      pixels[i + 1] = 100;
      pixels[i + 2] = 70;
      pixels[i + 3] = 255;
    }
  frames.push(pixels);
}
const bytes = encodeGIF(frames, 256, 256);
await writeFile(process.argv[2], bytes);
console.log(`GIF encoded: ${frames.length} frames, ${bytes.length} bytes`);
