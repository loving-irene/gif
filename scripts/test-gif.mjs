import { writeFile } from "node:fs/promises";
import {
  encodeGIF,
  stabilizeFrames,
} from "../internal/app/web/gif-worker.v4.js";
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
const bytes25 = encodeGIF(frames25, 128, 128);
await writeFile(process.argv[3], bytes25);
console.log(`GIF encoded: ${frames25.length} frames, ${bytes25.length} bytes`);

// 单帧偏移与尺寸突变应被向相邻帧轨迹校正，但输出尺寸和透明通道保持不变。
const jitterFrames = [];
for (let frame = 0; frame < 5; frame++) {
  const pixels = new Uint8ClampedArray(128 * 128 * 4);
  const jitter = frame === 2 ? 8 : frame * 2;
  const bodySize = frame === 2 ? 34 : 30;
  for (let y = 70 + jitter; y < 70 + jitter + bodySize; y++)
    for (let x = 45 + jitter; x < 45 + jitter + bodySize; x++) {
      const i = (y * 128 + x) * 4;
      pixels[i] = 180;
      pixels[i + 1] = 90;
      pixels[i + 2] = 60;
      pixels[i + 3] = 255;
    }
  jitterFrames.push(pixels);
}
const stable = stabilizeFrames(jitterFrames, 128, 128);
if (stable.length !== jitterFrames.length || stable.some((frame) => frame.length !== 128 * 128 * 4))
  throw new Error("frame stabilization changed frame dimensions");
const metrics = (pixels) => {
  let minX = 128,
    maxX = -1,
    maxY = -1,
    area = 0;
  for (let y = 0; y < 128; y++)
    for (let x = 0; x < 128; x++) {
      if (pixels[(y * 128 + x) * 4 + 3] < 128) continue;
      area++;
      minX = Math.min(minX, x);
      maxX = Math.max(maxX, x);
      maxY = Math.max(maxY, y);
    }
  return { centerX: (minX + maxX + 1) / 2, foot: maxY + 1, area };
};
const before = metrics(jitterFrames[2]),
  after = metrics(stable[2]),
  expected = { centerX: 66, foot: 106, area: 900 };
for (const key of Object.keys(expected))
  if (Math.abs(after[key] - expected[key]) >= Math.abs(before[key] - expected[key]))
    throw new Error(
      `frame stabilization did not reduce ${key} jitter: before=${before[key]}, after=${after[key]}, expected=${expected[key]}`,
    );
