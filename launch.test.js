"use strict";

const { test } = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const crypto = require("node:crypto");
const { ensureBinary } = require("./launch.js");
const { install } = require("./install.js");
const pkg = require("./package.json");

test("first launch downloads a verified binary and later launches reuse it", async (t) => {
  const root = await fs.mkdtemp(path.join(__dirname, ".test-launch-"));
  t.after(() => fs.rm(root, { recursive: true, force: true }));
  await fs.writeFile(path.join(root, "package.json"), JSON.stringify(pkg));
  const bytes = Buffer.alloc(64);
  bytes.writeUInt32LE(0xfeedfacf, 0);
  bytes.writeUInt32LE(0x01000007, 4);
  const checksum = crypto.createHash("sha256").update(bytes).digest("hex");
  let downloads = 0;
  const installer = (options) => {
    downloads++;
    return install({ ...options, platform: "darwin", arch: "x64", fetcher: async (url) => {
      return String(url).endsWith("SHA256SUMS")
        ? new Response(`${checksum}  betterzip-mcp-darwin-amd64\n`)
        : new Response(bytes);
    } });
  };
  const binary = await ensureBinary(root, installer);
  assert.deepEqual(await fs.readFile(binary), bytes);
  assert.equal(await ensureBinary(root, installer), binary);
  assert.equal(downloads, 1);
});

test("failed bootstrap stays failed and does not create an executable", async (t) => {
  const root = await fs.mkdtemp(path.join(__dirname, ".test-launch-"));
  t.after(() => fs.rm(root, { recursive: true, force: true }));
  const failure = new Error("controlled download failure");
  await assert.rejects(ensureBinary(root, async () => { throw failure; }), failure);
  await assert.rejects(fs.stat(path.join(root, "bin", "betterzip-mcp")), { code: "ENOENT" });
});
