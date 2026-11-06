import assert from "node:assert/strict";
import fs from "node:fs";
import vm from "node:vm";
import { frontendAsset } from "./frontend-source.mjs";

const source = fs.readFileSync(frontendAsset("app"), "utf8");
const functions = source.slice(source.indexOf("async function encode(sheet)"), source.indexOf("async function ensureAcceptedDraft()"));
const animation = new Blob(["GIF89a-native-animation"], { type: "image/gif" });
let canvasCalls = 0;
const stored = [];
const context = vm.createContext({
  animation,
  account: { id: "test-user" },
  dataBlob: () => animation,
  local: async (_store, _op, value) => stored.push(value),
  renderGallery: async () => {},
  setTaskText: () => {},
  motionSpec: () => ({ cols: 5, frames: 25, size: 128 }),
  createImageBitmap: async () => { canvasCalls++; throw new Error("static grid decoder reached"); },
});
vm.runInContext(functions, context);
assert.equal(await vm.runInContext("encode(animation)", context), animation);
await vm.runInContext(`saveMotion({image: "gif", gif: "gif"}, {id:"job", name:"wave", input:{selection:{category:"daily"}}})`, context);
assert.equal(stored.at(-1).gif, animation);
// Legacy restored work can have only its original GIF. Re-saving it must also
// preserve all frames instead of decoding just the first one through Canvas.
await vm.runInContext(`saveMotion({image: "gif"}, {id:"restored", name:"wave", input:{selection:{category:"daily"}}})`, context);
assert.equal(stored.at(-1).gif, animation);
assert.equal(canvasCalls, 0);
context.staticImage = new Blob(["PNG"], { type: "image/png" });
await assert.rejects(vm.runInContext("encode(staticImage)", context), /static grid decoder reached/);
assert.equal(canvasCalls, 1);
console.log("Native GIF frames preserved through saving and recovery; static images still use grid encoding");
