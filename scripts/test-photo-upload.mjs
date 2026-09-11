import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import vm from "node:vm";
import { File } from "node:buffer";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const source = fs
  .readFileSync(
    process.argv[2] || path.join(root, "internal/app/web/app.v6.js"),
    "utf8",
  )
  .replace(/^import[\s\S]*?from "\.\/common\.v2\.js";\s*/, "")
  .replace(/init\(\);\s*$/, "");
let decoded = 0,
  canvases = 0,
  closed = 0;
const nodes = new Map();
const $ = (id) => {
  if (!nodes.has(id)) nodes.set(id, { textContent: "" });
  return nodes.get(id);
};
const context = vm.createContext({
  $,
  shown: null,
  console,
  createImageBitmap: async () => {
    decoded++;
    return {
      width: 4000,
      height: 3000,
      close() {
        closed++;
      },
    };
  },
  document: {
    createElement(tag) {
      assert.equal(tag, "canvas");
      canvases++;
      return {
        width: 0,
        height: 0,
        getContext() {
          return { fillRect() {}, drawImage() {} };
        },
        toBlob(done, type) {
          done(new Blob([new Uint8Array(1024)], { type }));
        },
      };
    },
  },
});
vm.runInContext(source, context);
vm.runInContext(
  "showPhoto = blob => { shown=blob }; step=()=>{}; updateControls=()=>{};",
  context,
);
for (const type of ["image/jpeg", "image/png", "image/webp"]) {
  const file = new File([new Uint8Array([4, 3, 2, 1])], "selfie", { type });
  context.input = file;
  await vm.runInContext("preparePhoto(input)", context);
  assert.equal(
    vm.runInContext("selfie", context),
    file,
    "small upload must retain original File",
  );
  assert.equal(context.shown, file);
  assert.equal(decoded, 0, "small upload must not decode/re-encode");
  assert.equal(canvases, 0, "small upload must never use Canvas");
}
const exact = new File([new Uint8Array(5 * 1024 * 1024)], "exact.png", {
  type: "image/png",
});
context.input = exact;
await vm.runInContext("preparePhoto(input)", context);
assert.equal(
  vm.runInContext("selfie", context),
  exact,
  "exactly 5MiB stays unchanged",
);
assert.equal(decoded, 0);
const large = new File([new Uint8Array(5 * 1024 * 1024 + 1)], "large.jpg", {
  type: "image/jpeg",
});
context.input = large;
await vm.runInContext("preparePhoto(input)", context);
const compressed = vm.runInContext("selfie", context);
assert.notEqual(compressed, large);
assert.equal(decoded, 1);
assert.equal(canvases, 1);
assert.equal(closed, 1);
assert.ok(compressed.size <= 5 * 1024 * 1024);
console.log(
  "Photo upload passed: JPEG/PNG/WebP unchanged below and at 5MiB; compression only above threshold",
);
