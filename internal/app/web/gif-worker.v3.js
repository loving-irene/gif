// GIF89a encoder v3. Shared adaptive palette; disposal=2 clears each transparent frame.
// Literal LZW blocks reset before the code width grows, prioritizing correctness.
// 与 v1 的差别：支持动作序列图的两种规格——4×4 共16帧每帧256×256，5×5 共25帧每帧128×128；
// 展开阶段的提速区间随帧数切换（16帧为第5—8帧，25帧为第7—12帧）。
const SPEC_BY_FRAMES = { 16: { size: 256, fast: [4, 7] }, 25: { size: 128, fast: [6, 11] } };
export function encodeGIF(frames, width, height) {
  const spec = SPEC_BY_FRAMES[frames.length];
  if (!spec || width !== spec.size || height !== spec.size)
    throw new Error("动图必须为16帧每帧256×256，或25帧每帧128×128");
  const histogram = new Uint32Array(32768);
  for (const rgba of frames)
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
  for (let frame = 0; frame < frames.length; frame++) {
    const rgba = frames[frame];
    byte(0x21);
    byte(0xf9);
    byte(4);
    byte(9);
    word(
      frame >= spec.fast[0] && frame <= spec.fast[1] ? 6 : 10,
    );
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
