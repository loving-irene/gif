import fs from "node:fs/promises";

const url = "https://openrouter.ai/api/v1/images/models";
const response = await fetch(url, { signal: AbortSignal.timeout(30_000) });
if (!response.ok) throw new Error(`OpenRouter model discovery returned HTTP ${response.status}`);
const { data } = await response.json();
if (!Array.isArray(data) || !data.length) throw new Error("Empty OpenRouter model catalog");
const seen = new Set();
const models = data.map(({ id, name, supported_parameters }) => {
  if (typeof id !== "string" || !/^[a-z0-9][a-z0-9._/-]+$/i.test(id) ||
      typeof name !== "string" || /[<>]/.test(name) || seen.has(id) ||
      !supported_parameters || typeof supported_parameters !== "object") {
    throw new Error(`Invalid model record: ${id}`);
  }
  seen.add(id);
  return { id, name, supported_parameters };
});
if (!seen.has("openai/gpt-image-2.6-sunburst")) throw new Error("Configured default model missing; review before replacing catalog");
await fs.writeFile(new URL("../internal/app/openrouter_models.json", import.meta.url), JSON.stringify(models, null, 2) + "\n");
console.log(`Updated ${models.length} image model contracts from ${url}; run Go tests and review the diff before deployment.`);
