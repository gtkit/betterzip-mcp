"use strict";

// Exercise the installed plugin through Codex, without starting a model turn.
// Usage: node codex-smoke-test.js
// The ephemeral protocol context is not a saved chat and never starts a turn.
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const os = require("node:os");
const readline = require("node:readline");
const { spawn, spawnSync } = require("node:child_process");
const { createHash } = require("node:crypto");

// Normalize before serialization so quoted/Unicode paths are also removed.
function serializeEvidence(value, testRoot, projectRoot = __dirname, userHome = os.homedir()) {
  const locations = [[testRoot, "${TEST_ROOT}"], [projectRoot, "${PROJECT_ROOT}"], [userHome, "${USER_HOME}"]]
    .sort((a, b) => b[0].length - a[0].length);
  return `${JSON.stringify(value, (_key, item) => {
    if (typeof item !== "string") return item;
    for (const [absolute, label] of locations) item = item.replaceAll(absolute, label);
    return item;
  }, 2)}\n`;
}

class CodexClient {
  constructor(home) {
    const env = { ...process.env, HOME: home, CODEX_HOME: process.env.CODEX_HOME || path.join(os.homedir(), ".codex") };
    delete env.BETTERZIP_MCP_ROOT;
    this.child = spawn(process.env.CODEX_BIN || "codex", ["app-server", "--stdio"], {
      cwd: __dirname,
      env,
      stdio: ["pipe", "pipe", "pipe"],
    });
    this.pending = new Map();
    this.id = 0;
    this.stderr = "";
    this.child.stderr.on("data", (data) => { this.stderr = (this.stderr + data).slice(-65536); });
    this.child.on("error", (error) => this.fail(error));
    this.child.stdin.on("error", (error) => this.fail(error));
    const lines = readline.createInterface({ input: this.child.stdout });
    lines.on("line", (line) => {
      try {
        const message = JSON.parse(line);
        const pending = this.pending.get(message.id);
        if (!pending) return;
        this.pending.delete(message.id);
        clearTimeout(pending.timer);
        if (message.error) pending.reject(new Error(JSON.stringify(message.error)));
        else pending.resolve(message.result);
      } catch (error) { this.fail(error); }
    });
    this.closed = new Promise((resolve) => this.child.once("close", (code, signal) => {
      this.fail(new Error(`Codex exited ${code}/${signal}: ${this.stderr}`));
      lines.close();
      resolve({ code, signal });
    }));
  }

  fail(error) {
    for (const pending of this.pending.values()) {
      clearTimeout(pending.timer);
      pending.reject(error);
    }
    this.pending.clear();
  }

  request(method, params = {}) {
    const id = ++this.id;
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(id);
        reject(new Error(`Codex timed out: ${method}: ${this.stderr}`));
      }, 30000);
      this.pending.set(id, { resolve, reject, timer });
      this.child.stdin.write(`${JSON.stringify({ id, method, params })}\n`);
    });
  }

  async close() {
    this.child.stdin.end();
    const timer = setTimeout(() => this.child.kill("SIGTERM"), 3000);
    try { return await this.closed; } finally { clearTimeout(timer); }
  }
}

async function main() {
  const home = await fs.mkdtemp(path.join(__dirname, ".test-codex-"));
  const root = path.join(home, "data");
  await fs.mkdir(root);
  const env = { ...process.env, HOME: home };
  delete env.BETTERZIP_MCP_ROOT;
  const binary = path.join(__dirname, "bin/betterzip-mcp");
  let client;
  const evidence = { timestamp: new Date().toISOString(), ephemeral: true, results: [] };
  try {
    const setup = spawnSync(binary, ["setup", "--root", "~/data"], { env, encoding: "utf8" });
    assert.equal(setup.status, 0, setup.stderr);
    assert.equal(setup.stdout, "");
    const diagnosis = spawnSync(binary, ["--diagnose"], { env, encoding: "utf8" });
    assert.equal(diagnosis.status, 0, diagnosis.stderr);
    assert.match(diagnosis.stderr, /Root source: 用户设置/);
    evidence.rootSource = "用户设置";
    client = new CodexClient(home);
    const initialized = await client.request("initialize", {
      clientInfo: { name: "betterzip-plugin-smoke-test", version: "0.1.0" },
      capabilities: { experimentalApi: true },
    });
    evidence.codex = { userAgent: initialized.userAgent, platformFamily: initialized.platformFamily, platformOs: initialized.platformOs };
    client.child.stdin.write('{"method":"initialized","params":{}}\n');
    const { plugin } = await client.request("plugin/read", {
      marketplacePath: path.join(__dirname, ".agents/plugins/marketplace.json"),
      pluginName: "betterzip-mcp",
    });
    assert.equal(plugin.summary.installed, true);
    assert.equal(plugin.summary.enabled, true);
    assert.equal(plugin.summary.interface.composerIcon, path.join(__dirname, "icon.png"));
    evidence.pluginId = plugin.summary.id;
    evidence.icon = plugin.summary.interface.composerIcon;
    const { thread } = await client.request("thread/start", { cwd: __dirname, ephemeral: true });
    const threadId = thread.id;
    evidence.threadId = threadId;
    await client.request("config/mcpServer/reload");
    const status = await client.request("mcpServerStatus/list", { threadId, serverName: "betterzip", detail: "toolsAndAuthOnly" });
    const server = status.data.find((item) => item.name === "betterzip");
    assert.ok(server, "Codex did not discover the plugin MCP server");
    assert.equal(server.toolsError, null, server.toolsError);
    assert.equal(server.serverInfo.name, "betterzip-mcp");
    evidence.tools = Object.keys(server.tools).sort();
    assert.deepEqual(evidence.tools, ["betterzip_compress", "betterzip_encrypt_compress", "betterzip_extract", "betterzip_split_compress"]);
    evidence.serverInfo = server.serverInfo;

    const call = async (tool, args) => {
      const result = await client.request("mcpServer/tool/call", { threadId, server: "betterzip", tool, arguments: args });
      assert.notEqual(result.isError, true, JSON.stringify(result));
      assert.ok(result.content.length > 0);
      evidence.results.push({ tool, result });
      console.log(`PASS ${tool}`);
      return JSON.stringify(result);
    };
    const input = Buffer.from("Codex actual MCP round trip 中文\n", "utf8");
    await fs.writeFile(path.join(root, "hello.txt"), input);
    const compressed = await call("betterzip_compress", { working_dir: root, output_archive: "hello.zip", sources: ["hello.txt"] });
    assert.doesNotMatch(compressed, /会员|付费/);
    const extracted = await call("betterzip_extract", { archive_path: path.join(root, "hello.zip"), output_dir: path.join(root, "restored") });
    assert.doesNotMatch(extracted, /会员|付费/);
    assert.deepEqual(await fs.readFile(path.join(root, "restored", "hello.txt")), input);
    evidence.roundTripSHA256 = createHash("sha256").update(input).digest("hex");
    const common = { working_dir: root, sources: ["hello.txt"] };
    const encrypted = await call("betterzip_encrypt_compress", { ...common, output_archive: "private.zip", password: "smoke-only-never-echo" });
    assert.match(encrypted, /会员/);
    assert.doesNotMatch(encrypted, /smoke-only-never-echo/);
    const split = await call("betterzip_split_compress", { ...common, output_archive: "split.zip", split_size: "100m" });
    assert.match(split, /会员/);
    assert.deepEqual((await fs.readdir(root)).sort(), ["hello.txt", "hello.zip", "restored"]);
    evidence.membershipCreatedFiles = false;
    await fs.mkdir(path.join(__dirname, "dist"), { recursive: true });
    await fs.writeFile(path.join(__dirname, "dist/codex-mcp-validation.json"), serializeEvidence(evidence, root));
    console.log("PASS ZIP bytes, membership no-op, icon metadata; evidence: dist/codex-mcp-validation.json");
  } finally {
    if (client) await client.close();
    await fs.rm(home, { recursive: true, force: true });
  }
}

module.exports = { serializeEvidence };

if (require.main === module) {
  main().catch((error) => { console.error(error); process.exitCode = 1; });
}
