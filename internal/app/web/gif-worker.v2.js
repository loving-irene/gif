// GIF89a encoder v2. 与服务器端 synthesizeGIF 保持一致：
// 16 格源帧两两补插为 32 帧（预乘 alpha 混合、轮廓并集），共享调色板 + Floyd–Steinberg
// 误差扩散抖动，每帧 50ms（20fps），disposal=2 逐帧清空透明区域。
export function encodeGIF(frames, width, height) {
  if (frames.length !== 16 || width !== 256 || height !== 256)
    throw new Error("动图必须为16帧，每帧256×256");
  const pixelCount = width * height * 4;
  // 1) 补插：16 源帧 → 32 输出帧。偶数帧为源帧原样，奇数帧为相邻两帧的并集轮廓混合。
  const interpolated = [];
  for (let n = 0; n < 32; n++) {
    const pos = (n * 16) / 32;
    const a = Math.floor(pos) % 16;
    const f = pos - a;
    if (f === 0) {
      interpolated.push(frames[a]);
      continue;
    }
    const b = (a + 1) % 16;
    const fa = frames[a];
    const fb = frames[b];
    const out = new Uint8ClampedArray(pixelCount);
    for (let i = 0; i < pixelCount; i += 4) {
      const aA = fa[i + 3];
      const bA = fb[i + 3];
      out[i + 3] = Math.max(aA, bA);
      const w = aA * (1 - f) + bA * f;
      if (w <= 0) continue;
      out[i] = (fa[i] * aA * (1 - f) + fb[i] * bA * f) / w;
      out[i + 1] = (fa[i + 1] * aA * (1 - f) + fb[i + 1] * bA * f) / w;
      out[i + 2] = (fa[i + 2] * aA * (1 - f) + fb[i + 2] * bA * f) / w;
    }
    interpolated.push(out);
  }
  const rgbaFrames = interpolated;
  // 2) 15-bit 直方图（32 帧全部参与）。
  const histogram = new Uint32Array(32768);
  for (const rgba of rgbaFrames)
    for (let i = 0; i < rgba.length; i += 4)
      if (rgba[i + 3] >= 128)
        histogram[
          ((rgba[i] >> 3) << 10) |
            ((rgba[i + 1] >> 3) << 5) |
            (rgba[i + 2] >> 3)
        ]++;
  // 3) 中位切分共享调色板。
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
  const palette = [[0, 0, 0]];
  for (const box of boxes) {
    const total = box.p.reduce((n, x) => n + x.n, 0);
    const color = ["r", "g", "b"].map((axis) =>
      Math.round(box.p.reduce((n, x) => n + x[axis] * x.n, 0) / total),
    );
    palette.push(color);
  }
  while (palette.length < 256) palette.push([0, 0, 0]);
  // 4) 最近色查找表（跳过 0 号透明位）。
  const nearest = new Uint8Array(32768);
  for (let k = 0; k < 32768; k++) {
    const r = (k >> 10) * 8 + 4,
      g = ((k >> 5) & 31) * 8 + 4,
      b = (k & 31) * 8 + 4;
    let best = 1,
      bestD = Infinity;
    for (let i = 1; i < palette.length; i++) {
      const dr = palette[i][0] - r,
        dg = palette[i][1] - g,
        db = palette[i][2] - b;
      const d = dr * dr + dg * dg + db * db;
      if (d < bestD) {
        bestD = d;
        best = i;
      }
    }
    nearest[k] = best;
  }
  // 5) Floyd–Steinberg 误差扩散：透明像素保持 0 号索引且不参与扩散。
  const indexed = rgbaFrames.map((rgba) => {
    const idx = new Uint8Array(width * height);
    const curR = new Float64Array(width + 2),
      curG = new Float64Array(width + 2),
      curB = new Float64Array(width + 2);
    const nextR = new Float64Array(width + 2),
      nextG = new Float64Array(width + 2),
      nextB = new Float64Array(width + 2);
    for (let y = 0; y < height; y++) {
      for (let x = 0; x < width + 2; x++) {
        curR[x] = nextR[x];
        curG[x] = nextG[x];
        curB[x] = nextB[x];
        nextR[x] = 0;
        nextG[x] = 0;
        nextB[x] = 0;
      }
      for (let x = 0; x < width; x++) {
        const i = (y * width + x) * 4;
        const o = y * width + x;
        if (rgba[i + 3] < 128) {
          idx[o] = 0;
          curR[x + 1] = 0;
          curG[x + 1] = 0;
          curB[x + 1] = 0;
          continue;
        }
        const r = Math.min(255, Math.max(0, rgba[i] + curR[x + 1]));
        const g = Math.min(255, Math.max(0, rgba[i + 1] + curG[x + 1]));
        const b = Math.min(255, Math.max(0, rgba[i + 2] + curB[x + 1]));
        const index = nearest[((r >> 3) << 10) | ((g >> 3) << 5) | (b >> 3)];
        idx[o] = index;
        const er = r - palette[index][0],
          eg = g - palette[index][1],
          eb = b - palette[index][2];
        curR[x + 2] += (er * 7) / 16;
        curG[x + 2] += (eg * 7) / 16;
        curB[x + 2] += (eb * 7) / 16;
        nextR[x] += (er * 3) / 16;
        nextG[x] += (eg * 3) / 16;
        nextB[x] += (eb * 3) / 16;
        nextR[x + 1] += (er * 5) / 16;
        nextG[x + 1] += (eg * 5) / 16;
        nextB[x + 1] += (eb * 5) / 16;
        nextR[x + 2] += er / 16;
        nextG[x + 2] += eg / 16;
        nextB[x + 2] += eb / 16;
      }
    }
    return idx;
  });
  // 6) 编码：统一 50ms 每帧。LZW 块在码宽增长前重置，保证解码正确性。
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
  for (let frame = 0; frame < indexed.length; frame++) {
    const idx = indexed[frame];
    byte(0x21);
    byte(0xf9);
    byte(4);
    byte(9);
    word(5);
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
    for (let o = 0; o < idx.length; o++) {
      if (count === 200) {
        code(256);
        count = 0;
      }
      code(idx[o]);
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
