#!/usr/bin/env node
"use strict";

const fs = require("node:fs/promises");
const path = require("node:path");
const os = require("node:os");
const { spawn } = require("node:child_process");
const { install, platformArch } = require("./install.js");

async function main() {
  platformArch();
  const binary = path.join(__dirname, "bin", "betterzip-mcp");
  try {
    await fs.access(binary, fs.constants.X_OK);
  } catch (error) {
    if (error.code !== "ENOENT") throw error;
    await install(); // Local Codex plugin bootstrap; npm normally installed it already.
  }
  const child = spawn(binary, process.argv.slice(2), { stdio: "inherit", shell: false });
  const handlers = new Map();
  for (const signal of ["SIGINT", "SIGTERM", "SIGHUP"]) {
    const handler = () => child.kill(signal);
    handlers.set(signal, handler);
    process.on(signal, handler);
  }
  let spawnFailed = false;
  child.once("error", (error) => {
    spawnFailed = true;
    process.stderr.write(`betterzip-mcp: cannot start binary: ${error.message}\n`);
  });
  child.once("close", (code, signal) => {
    for (const [name, handler] of handlers) process.removeListener(name, handler);
    process.exitCode = spawnFailed ? 1 : signal ? 128 + (os.constants.signals[signal] || 1) : (code ?? 1);
  });
}

if (require.main === module) {
  main().catch((error) => {
    process.stderr.write(`betterzip-mcp: ${error.message}\n`);
    process.exitCode = 1;
  });
}

module.exports = { main };
