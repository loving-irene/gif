import assert from "node:assert/strict";
import fs from "node:fs";
import vm from "node:vm";
import { File } from "node:buffer";
import { frontendAsset } from "./frontend-source.mjs";

const source = fs
  .readFileSync(process.argv[2] || frontendAsset("app"), "utf8")
  .replace(/^import[\s\S]*?from "\.\/common\.v\d+\.js";\s*/, "")
  .replace(/init\(\);\s*$/, "");

const nodes = new Map();
const $ = (id) => {
  if (!nodes.has(id)) {
    nodes.set(id, {
      textContent: "",
      hidden: false,
      disabled: false,
      classList: { toggle() {} },
      replaceChildren() {},
      append() {},
      children: [],
    });
  }
  return nodes.get(id);
};

const toasts = [];
const draftBlob = new Blob([new Uint8Array([1, 2, 3])], { type: "image/png" });
const context = vm.createContext({
  $,
  shown: null,
  console,
  account: { id: "selfie-draft-reset" },
  catalog: { configured: true, estimates: {}, userConcurrency: 5 },
  local: async () => {},
  toast: (msg) => toasts.push(msg),
  userNotice: (msg) => {
    const err = new Error(msg);
    err.userFacing = true;
    return err;
  },
  draftBlob,
  dataBlob: () => draftBlob,
  document: {
    createElement() {
      return {
        classList: { toggle() {} },
        setAttribute() {},
        append() {},
      };
    },
  },
  URL: {
    createObjectURL: () => "blob:test",
    revokeObjectURL() {},
  },
  crypto: { randomUUID: () => "uuid" },
  Blob,
});

vm.runInContext(source, context);
vm.runInContext(
  `
  showPhoto = blob => { shown = blob };
  step = () => {};
  updateControls = () => {};
  renderCandidates = () => { renderedCandidates = (profile?.candidates || []).length };
  showActions = () => {};
  `,
  context,
);

const first = new File([new Uint8Array([9, 9, 9])], "one.jpg", {
  type: "image/jpeg",
});
context.input = first;
await vm.runInContext("preparePhoto(input)", context);

vm.runInContext(
  `
  profile.draft = draftBlob;
  profile.receipt = "receipt-old";
  profile.accepted = true;
  profile.selection = { category: "daily" };
  profile.candidates = [{
    draft: draftBlob,
    receipt: "receipt-old",
    selection: { category: "daily" },
    accepted: true,
    selfieGeneration,
  }];
  profile.selfie = selfie;
  profile.selfieGeneration = selfieGeneration;
  `,
  context,
);
assert.equal(vm.runInContext("hasUsableDraft()", context), true);

const second = new File([new Uint8Array([8, 8, 8])], "two.jpg", {
  type: "image/jpeg",
});
context.input = second;
await vm.runInContext("preparePhoto(input)", context);

assert.equal(vm.runInContext("selfie", context), second);
assert.equal(vm.runInContext("hasUsableDraft()", context), false);
assert.equal(vm.runInContext("needsDraft()", context), true);
assert.equal(vm.runInContext("profile.draft", context), null);
assert.equal(vm.runInContext("profile.receipt", context), "");
assert.equal(vm.runInContext("profile.accepted", context), false);
assert.equal(vm.runInContext("(profile.candidates || []).length", context), 0);
assert.ok(vm.runInContext("selfieGeneration", context) >= 1);
assert.equal(
  vm.runInContext("renderedCandidates", context),
  0,
  "candidate strip must clear after replacing selfie",
);

await vm.runInContext(
  `(async () => {
    staleTask = { selfieGeneration: selfieGeneration - 1, input: { selection: {} } };
    await receiveDraft({ image: "data:image/png;base64,aaaa", receipt: "stale" }, staleTask);
  })()`,
  context,
);
assert.equal(vm.runInContext("profile.draft", context), null);
assert.ok(toasts.some((t) => String(t).includes("已忽略上一张照片的定稿结果")));

console.log(
  "Selfie draft reset passed: replacing selfie clears drafts/candidates and ignores stale draft jobs",
);
