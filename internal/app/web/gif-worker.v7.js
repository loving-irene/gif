// GIF89a encoder v6. Stabilized subject motion, shared adaptive palette and spec timing.
// Literal LZW blocks reset before the code width grows, prioritizing correctness.
// 16帧保持80ms；25帧使用70ms；100帧使用20ms，使完整动作仍约2秒且播放达到50 FPS。
const SPEC_BY_FRAMES = {
  16: { size: 256, delay: 8 },
  25: { size: 128, delay: 7 },
  100: { size: 256, delay: 2 },
};

const clamp = (value, low, high) => Math.max(low, Math.min(high, value));
const median = (values) => {
  const sorted = [...values].sort((a, b) => a - b);
  const middle = Math.floor(sorted.length / 2);
  return sorted.length % 2
    ? sorted[middle]
    : (sorted[middle - 1] + sorted[middle]) / 2;
};

function measureSubject(rgba, width, height) {
  let minX = width,
    maxX = -1,
    maxY = -1,
    area = 0;
  for (let y = 0; y < height; y++)
    for (let x = 0; x < width; x++) {
      if (rgba[(y * width + x) * 4 + 3] < 128) continue;
      area++;
      minX = Math.min(minX, x);
      maxX = Math.max(maxX, x);
      maxY = Math.max(maxY, y);
    }
  return area
    ? { valid: true, centerX: (minX + maxX + 1) / 2, foot: maxY + 1, area }
    : { valid: false };
}

function transformedFrame(source, width, height, current, targetX, targetFoot, scale) {
  const output = new Uint8ClampedArray(source.length);
  for (let y = 0; y < height; y++)
    for (let x = 0; x < width; x++) {
      // 以人物水平中心和脚底为锚点做逆向采样，防止缩放时重新引入位置漂移。
      const sx = current.centerX + (x + 0.5 - targetX) / scale - 0.5;
      const sy = current.foot + (y + 0.5 - targetFoot) / scale - 0.5;
      if (sx < 0 || sy < 0 || sx >= width || sy >= height) continue;
      const x0 = Math.floor(sx),
        y0 = Math.floor(sy),
        x1 = Math.min(width - 1, x0 + 1),
        y1 = Math.min(height - 1, y0 + 1),
        fx = sx - x0,
        fy = sy - y0,
        i00 = (y0 * width + x0) * 4,
        i10 = (y0 * width + x1) * 4,
        i01 = (y1 * width + x0) * 4,
        i11 = (y1 * width + x1) * 4,
        a00 = source[i00 + 3] * (1 - fx) * (1 - fy),
        a10 = source[i10 + 3] * fx * (1 - fy),
        a01 = source[i01 + 3] * (1 - fx) * fy,
        a11 = source[i11 + 3] * fx * fy,
        alpha = a00 + a10 + a01 + a11,
        red =
          source[i00] * a00 +
          source[i10] * a10 +
          source[i01] * a01 +
          source[i11] * a11,
        green =
          source[i00 + 1] * a00 +
          source[i10 + 1] * a10 +
          source[i01 + 1] * a01 +
          source[i11 + 1] * a11,
        blue =
          source[i00 + 2] * a00 +
          source[i10 + 2] * a10 +
          source[i01 + 2] * a01 +
          source[i11 + 2] * a11;
      const at = (y * width + x) * 4;
      output[at + 3] = alpha;
      if (alpha > 0) {
        output[at] = red / alpha;
        output[at + 1] = green / alpha;
        output[at + 2] = blue / alpha;
      }
    }
  return output;
}

// 循环五帧局部中位数能过滤单帧抖动，同时保留连续横移、起跳和落地；
// 8% 平移与 6% 缩放限幅用于避免武器或特效改变透明包围盒时发生过度校正。
export function stabilizeFrames(frames, width, height) {
  if (frames.length < 3) return frames;
  const metrics = frames.map((frame) => measureSubject(frame, width, height));
  const maxShift = width * 0.08;
  return frames.map((frame, index) => {
    const current = metrics[index];
    if (!current.valid) return frame;
    const nearby = [];
    for (let offset = -2; offset <= 2; offset++) {
      const item = metrics[(index + offset + frames.length) % frames.length];
      if (item.valid) nearby.push(item);
    }
    const targetX = current.centerX + clamp(
      median(nearby.map((item) => item.centerX)) - current.centerX,
      -maxShift,
      maxShift,
    );
    const targetFoot = current.foot + clamp(
      median(nearby.map((item) => item.foot)) - current.foot,
      -maxShift,
      maxShift,
    );
    const targetArea = median(nearby.map((item) => item.area));
    const scale = clamp(Math.sqrt(targetArea / current.area), 0.94, 1.06);
    if (targetX === current.centerX && targetFoot === current.foot && scale === 1)
      return frame;
    return transformedFrame(frame, width, height, current, targetX, targetFoot, scale);
  });
}

export function encodeGIF(frames, width, height) {
  const spec = SPEC_BY_FRAMES[frames.length];
  if (!spec || width !== spec.size || height !== spec.size)
    throw new Error(
      "动图必须为16帧每帧256×256、25帧每帧128×128，或100帧每帧256×256",
    );
  const stableFrames = stabilizeFrames(frames, width, height);
  const histogram = new Uint32Array(32768);
  for (const rgba of stableFrames)
    for (let i = 0; i < rgba.length; i += 4) {
      if (rgba[i + 3] >= 128)
        histogram[
          ((rgba[i] >> 3) << 10) |
            ((rgba[i + 1] >> 3) << 5) |
            (rgba[i + 2] >> 3)
        ]++;
    }
  const points = [];
  for (let k = 0; k < histogram.length; k++)
    if (histogram[k])
      points.push({
        k,
        r: (k >> 10) * 8 + 4,
        g: ((k >> 5) & 31) * 8 + 4,
        b: (k & 31) * 8 + 4,
        n: histogram[k],
      });
  const measure = (p) => {
    const bounds = ["r", "g", "b"].map((axis) => {
      let lo = 255,
        hi = 0;
      for (const v of p) {
        lo = Math.min(lo, v[axis]);
        hi = Math.max(hi, v[axis]);
      }
      return hi - lo;
    });
    const max = Math.max(...bounds);
    return {
      p,
      axis: ["r", "g", "b"][bounds.indexOf(max)],
      score: max * p.reduce((n, x) => n + x.n, 0),
    };
  };
  let boxes = points.length ? [measure(points)] : [];
  while (boxes.length < 255) {
    boxes.sort((a, b) => b.score - a.score);
    const index = boxes.findIndex((b) => b.p.length > 1);
    if (index < 0) break;
    const box = boxes.splice(index, 1)[0];
    box.p.sort((a, b) => a[box.axis] - b[box.axis]);
    const total = box.p.reduce((n, x) => n + x.n, 0);
    let sum = 0,
      split = 1;
    for (; split < box.p.length; split++) {
      sum += box.p[split - 1].n;
      if (sum >= total / 2) break;
    }
    split = Math.min(split, box.p.length - 1);
    boxes.push(measure(box.p.slice(0, split)), measure(box.p.slice(split)));
  }
  const palette = [[0, 0, 0]],
    mapping = new Uint8Array(32768);
  for (const box of boxes) {
    const total = box.p.reduce((n, x) => n + x.n, 0);
    const color = ["r", "g", "b"].map((axis) =>
      Math.round(box.p.reduce((n, x) => n + x[axis] * x.n, 0) / total),
    );
    const index = palette.length;
    palette.push(color);
    for (const p of box.p) mapping[p.k] = index;
  }
  while (palette.length < 256) palette.push([0, 0, 0]);
  const out = [];
  const byte = (v) => out.push(v & 255);
  const word = (v) => {
    byte(v);
    byte(v >> 8);
  };
  const text = (s) => {
    for (const c of s) byte(c.charCodeAt(0));
  };
  text("GIF89a");
  word(width);
  word(height);
  byte(0xf7);
  byte(0);
  byte(0);
  for (const rgb of palette) for (const c of rgb) byte(c);
  byte(0x21);
  byte(0xff);
  byte(11);
  text("NETSCAPE2.0");
  byte(3);
  byte(1);
  word(0);
  byte(0);
  for (let frame = 0; frame < stableFrames.length; frame++) {
    const rgba = stableFrames[frame];
    byte(0x21);
    byte(0xf9);
    byte(4);
    byte(9);
    word(spec.delay);
    byte(0);
    byte(0);
    byte(0x2c);
    word(0);
    word(0);
    word(width);
    word(height);
    byte(0);
    byte(8);
    const packed = [];
    let bits = 0,
      value = 0;
    const code = (c) => {
      value |= c << bits;
      bits += 9;
      while (bits >= 8) {
        packed.push(value & 255);
        value >>>= 8;
        bits -= 8;
      }
    };
    code(256);
    let count = 0;
    for (let i = 0; i < rgba.length; i += 4) {
      if (count === 200) {
        code(256);
        count = 0;
      }
      const index =
        rgba[i + 3] < 128
          ? 0
          : mapping[
              ((rgba[i] >> 3) << 10) |
                ((rgba[i + 1] >> 3) << 5) |
                (rgba[i + 2] >> 3)
            ];
      code(index);
      count++;
    }
    code(257);
    if (bits) packed.push(value & 255);
    for (let i = 0; i < packed.length; i += 255) {
      const n = Math.min(255, packed.length - i);
      byte(n);
      for (let j = 0; j < n; j++) byte(packed[i + j]);
    }
    byte(0);
  }
  byte(0x3b);
  return new Uint8Array(out);
}
if (typeof self !== "undefined")
  self.onmessage = (e) => {
    try {
      const { frames, width, height } = e.data;
      const bytes = encodeGIF(
        frames.map((x) => new Uint8ClampedArray(x)),
        width,
        height,
      );
      self.postMessage({ bytes: bytes.buffer }, [bytes.buffer]);
    } catch (error) {
      self.postMessage({ error: error.message });
    }
  };
