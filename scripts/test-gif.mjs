import { writeFile } from "node:fs/promises";
import { encodeGIF } from "../internal/app/web/gif-worker.v1.js";
import { encodeGIF as encodeV3 } from "../internal/app/web/gif-worker.v3.js";
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
// 5×5 规格：25 帧每帧 128×128，使用 v3 编码器交叉验证。
const frames25 = [];
for (let frame = 0; frame < 25; frame++) {
  const pixels = new Uint8ClampedArray(128 * 128 * 4);
  const shift = Math.floor(frame * 4);
  for (let y = 15; y < 45; y++)
    for (let x = 5 + shift; x < 25 + shift; x++) {
      const i = (y * 128 + x) * 4;
      pixels[i] = 220;
      pixels[i + 1] = 100;
      pixels[i + 2] = 70;
      pixels[i + 3] = 255;
    }
  frames25.push(pixels);
}
const bytes25 = encodeV3(frames25, 128, 128);
await writeFile(process.argv[3], bytes25);
console.log(`GIF encoded: ${frames25.length} frames, ${bytes25.length} bytes`);
