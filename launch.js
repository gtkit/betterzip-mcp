#!/usr/bin/env node
"use strict";

const fs = require("node:fs/promises");
const path = require("node:path");
const os = require("node:os");
const { spawn } = require("node:child_process");
const { install, platformArch } = require("./install.js");

async function ensureBinary(packageRoot = __dirname, installer = install) {
  const binary = path.join(packageRoot, "bin", "betterzip-mcp");
  try {
    await fs.access(binary, fs.constants.X_OK);
  } catch (error) {
    if (error.code !== "ENOENT") throw error;
    process.stderr.write("betterzip-mcp: 正在下载并校验运行程序，完成后会自动启动，无需手动下载。\n");
    await installer({ packageRoot });
    await fs.access(binary, fs.constants.X_OK);
  }
  return binary;
}

async function main(packageRoot = __dirname, args = process.argv.slice(2)) {
  platformArch();
  const binary = await ensureBinary(packageRoot);
  const child = spawn(binary, args, { stdio: "inherit", shell: false });
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

module.exports = { main, ensureBinary };
