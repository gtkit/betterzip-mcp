"use strict";

const { test } = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { spawn, spawnSync } = require("node:child_process");
const { setTimeout: delay } = require("node:timers/promises");

const binary = process.env.BETTERZIP_TEST_BINARY
  ? path.resolve(__dirname, process.env.BETTERZIP_TEST_BINARY)
  : path.join(__dirname, "bin", "betterzip-mcp");

class Client {
  constructor(root, { launcher = false, packageRoot = __dirname, args = [], explicitRoot = true, env = process.env } = {}) {
    const command = launcher ? process.execPath : binary;
    const launchArgs = launcher
      ? ["-e", 'require(process.env.BETTERZIP_TEST_LAUNCHER).main(process.env.BETTERZIP_TEST_PACKAGE, process.argv.slice(1)).catch(e => { console.error(e); process.exitCode = 1; });', "--"]
      : [];
    const rootArgs = explicitRoot ? ["--root", root] : [];
    this.child = spawn(command, [...launchArgs, ...rootArgs, ...args], {
      cwd: __dirname,
      env: { ...env, BETTERZIP_TEST_LAUNCHER: path.join(__dirname, "launch.js"), BETTERZIP_TEST_PACKAGE: packageRoot },
      stdio: ["pipe", "pipe", "pipe"],
    });
    this.pending = new Map();
    this.nextID = 1;
    this.buffer = "";
    this.stderr = "";
    this.child.stdout.setEncoding("utf8");
    this.child.stdout.on("data", (data) => {
      this.buffer += data;
      let index;
      while ((index = this.buffer.indexOf("\n")) >= 0) {
        const line = this.buffer.slice(0, index);
        this.buffer = this.buffer.slice(index + 1);
        try {
          const message = JSON.parse(line);
          assert.equal(message.jsonrpc, "2.0");
          const pending = this.pending.get(message.id);
          if (pending) {
            clearTimeout(pending.timer);
            this.pending.delete(message.id);
            if (message.error) pending.reject(new Error(JSON.stringify(message.error)));
            else pending.resolve(message.result);
          }
        } catch (error) { this.fail(error); }
      }
    });
    this.child.stderr.on("data", (data) => { this.stderr = (this.stderr + data).slice(-65536); });
    this.child.on("error", (error) => this.fail(error));
    this.child.stdin.on("error", (error) => this.fail(error));
    this.done = new Promise((resolve) => this.child.on("close", (code, signal) => {
      this.fail(new Error(`server closed: ${code}/${signal}: ${this.stderr}`));
      resolve({ code, signal });
    }));
  }

  fail(error) {
    for (const pending of this.pending.values()) { clearTimeout(pending.timer); pending.reject(error); }
    this.pending.clear();
  }

  request(method, params = {}, id = this.nextID++) {
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => { this.pending.delete(id); reject(new Error(`request timeout: ${method}: ${this.stderr}`)); }, 10000);
      this.pending.set(id, { resolve, reject, timer });
      this.child.stdin.write(`${JSON.stringify({ jsonrpc: "2.0", id, method, params })}\n`);
    });
  }

  notify(method, params = {}) { this.child.stdin.write(`${JSON.stringify({ jsonrpc: "2.0", method, params })}\n`); }

  async initialize() {
    const result = await this.request("initialize", { protocolVersion: "2025-03-26", capabilities: {}, clientInfo: { name: "betterzip-functional-test", version: "1" } });
    assert.equal(result.serverInfo.name, "betterzip-mcp");
    this.notify("notifications/initialized");
  }

  async close() {
    this.child.stdin.end();
    const timer = setTimeout(() => this.child.kill("SIGKILL"), 3000);
    try { return await this.done; } finally { clearTimeout(timer); }
  }
}

async function fixture(t) {
  await fs.access(binary);
  const root = await fs.mkdtemp(path.join(__dirname, ".test-protocol-"));
  const packageRoot = path.join(root, "package");
  await fs.mkdir(path.join(packageRoot, "bin"), { recursive: true });
  await fs.symlink(path.relative(path.join(packageRoot, "bin"), binary), path.join(packageRoot, "bin", "betterzip-mcp"));
  const clients = [];
  t.after(async () => {
    for (const client of clients) await client.close();
    await fs.rm(root, { recursive: true, force: true });
  });
  return { root, client: (options) => { const client = new Client(root, { packageRoot, ...options }); clients.push(client); return client; } };
}

async function helperStarted(marker) {
  for (let count = 0; count < 250; count += 1) {
    try { return Number(await fs.readFile(marker, "utf8")); }
    catch (error) { if (error.code !== "ENOENT") throw error; }
    await delay(10);
  }
  throw new Error("controlled helper did not start");
}

async function blockingHelper(root) {
  const marker = path.join(root, "started");
  const helper = path.join(root, "blocking-helper");
  await fs.writeFile(helper, '#!/bin/sh\nprintf \'%s\' "$$" > "$(dirname "$0")/started"\nexec /bin/sleep 60\n', { mode: 0o755 });
  await fs.writeFile(path.join(root, "fixture.zip"), "controlled fixture, not a real archive");
  return { helper, marker };
}

test("stdio initializes, discovers five tools and performs a real ZIP round trip", async (t) => {
  const { root, client } = await fixture(t);
  await fs.writeFile(path.join(root, "hello.txt"), "stdio round trip 中文\n");
  const c = client();
  await c.initialize();
  const listed = await c.request("tools/list");
  assert.deepEqual(listed.tools.map((tool) => tool.name).sort(), ["betterzip_compress", "betterzip_configure", "betterzip_encrypt_compress", "betterzip_extract", "betterzip_split_compress"]);
  for (const tool of listed.tools) assert.equal(tool.inputSchema.additionalProperties, false);
  const compressed = await c.request("tools/call", { name: "betterzip_compress", arguments: { working_dir: ".", output_archive: "hello.zip", sources: ["hello.txt"] } });
  assert.notEqual(compressed.isError, true);
  assert.doesNotMatch(JSON.stringify(compressed), /会员|付费/);
  const extracted = await c.request("tools/call", { name: "betterzip_extract", arguments: { archive_path: "hello.zip", output_dir: "out" } });
  assert.notEqual(extracted.isError, true);
  assert.equal(await fs.readFile(path.join(root, "out", "hello.txt"), "utf8"), "stdio round trip 中文\n");
  const member = await c.request("tools/call", { name: "betterzip_encrypt_compress", arguments: { working_dir: "/unavailable", output_archive: "unused.zip", sources: ["missing"], password: "never-echo-this" } });
  assert.notEqual(member.isError, true);
  assert.doesNotMatch(JSON.stringify(member), /never-echo-this/);
  const invalid = await c.request("tools/call", { name: "betterzip_compress", arguments: { working_dir: ".", output_archive: "bad.zip", sources: ["../outside"] } });
  assert.equal(invalid.isError, true);
  assert.equal((await c.close()).code, 0);
});

test("MCP cancellation through the Node launcher terminates the active helper", async (t) => {
  const { root, client } = await fixture(t);
  const { helper, marker } = await blockingHelper(root);
  const c = client({ launcher: true, args: ["--sevenzip", helper] });
  await c.initialize();
  const pending = c.request("tools/call", { name: "betterzip_extract", arguments: { archive_path: "fixture.zip", output_dir: "out" } }, 100);
  const pid = await helperStarted(marker);
  c.notify("notifications/cancelled", { requestId: 100, reason: "functional test" });
  assert.equal((await pending).isError, true);
  assert.throws(() => process.kill(pid, 0), { code: "ESRCH" });
  await assert.rejects(fs.stat(path.join(root, "out")), { code: "ENOENT" });
  assert.equal((await c.close()).code, 0);
});

test("stdin EOF cancels work before clean server shutdown", async (t) => {
  const { root, client } = await fixture(t);
  const { helper, marker } = await blockingHelper(root);
  const c = client({ args: ["--sevenzip", helper] });
  await c.initialize();
  const pending = c.request("tools/call", { name: "betterzip_extract", arguments: { archive_path: "fixture.zip", output_dir: "out" } });
  pending.catch(() => {}); // A disconnected client need not receive its result.
  const pid = await helperStarted(marker);
  assert.equal((await c.close()).code, 0);
  assert.throws(() => process.kill(pid, 0), { code: "ESRCH" });
});

test("version, help and diagnosis keep stdout free of non-protocol output", async () => {
  for (const command of [[binary, ["--version"]], [process.execPath, [path.join(__dirname, "launch.js"), "--version"]], [binary, ["--help"]], [binary, ["--diagnose", "--root", __dirname]]]) {
    const result = spawnSync(command[0], command[1], { encoding: "utf8" });
    assert.equal(result.status, 0, result.stderr);
    assert.equal(result.stdout, "");
    assert.notEqual(result.stderr, "");
  }
});

test("unconfigured MCP advertises configuration without granting an implicit directory", async (t) => {
  const { root, client } = await fixture(t);
  const env = { ...process.env, HOME: root };
  delete env.BETTERZIP_MCP_ROOT;
  await fs.writeFile(path.join(root, "hello.txt"), "not authorized yet");
  const c = client({ explicitRoot: false, env });
  await c.initialize();
  assert.equal((await c.request("tools/list")).tools.length, 5);
  const result = await c.request("tools/call", { name: "betterzip_configure", arguments: { root } });
  assert.equal(result.isError, true);
  assert.match(JSON.stringify(result), /JSON schema/);
  const member = await c.request("tools/call", { name: "betterzip_split_compress", arguments: { working_dir: root, output_archive: "split.zip", sources: ["hello.txt"], split_size: "100m" } });
  assert.notEqual(member.isError, true);
  assert.deepEqual((await fs.readdir(root)).sort(), ["hello.txt", "package"]);
});

test("setup persists the selected directory across launcher restarts", async (t) => {
  const { root, client } = await fixture(t);
  const env = { ...process.env, HOME: root };
  delete env.BETTERZIP_MCP_ROOT;
  await fs.mkdir(path.join(root, "data"));
  await fs.writeFile(path.join(root, "data/hello.txt"), "persistent root\n");
  const setup = spawnSync(binary, ["setup", "--root", "~/data"], { env, encoding: "utf8" });
  assert.equal(setup.status, 0, setup.stderr);
  assert.equal(setup.stdout, "");
  const c = client({ explicitRoot: false, launcher: true, env });
  await c.initialize();
  const compressed = await c.request("tools/call", { name: "betterzip_compress", arguments: { working_dir: ".", output_archive: "ok.zip", sources: ["hello.txt"] } });
  assert.notEqual(compressed.isError, true, JSON.stringify(compressed));
  const extracted = await c.request("tools/call", { name: "betterzip_extract", arguments: { archive_path: "ok.zip", output_dir: "out" } });
  assert.notEqual(extracted.isError, true, JSON.stringify(extracted));
  assert.equal(await fs.readFile(path.join(root, "data/out/hello.txt"), "utf8"), "persistent root\n");
  await fs.mkdir(path.join(root, "next"));
  await fs.writeFile(path.join(root, "next/hello.txt"), "changed root\n");
  const changed = spawnSync(binary, ["setup", "--root", "~/next"], { env, encoding: "utf8" });
  assert.equal(changed.status, 0, changed.stderr);
  const next = await c.request("tools/call", { name: "betterzip_compress", arguments: { working_dir: ".", output_archive: "next.zip", sources: ["hello.txt"] } });
  assert.notEqual(next.isError, true, JSON.stringify(next));
  await fs.access(path.join(root, "next/next.zip"));
  await assert.rejects(fs.stat(path.join(root, "data/next.zip")), { code: "ENOENT" });
});
