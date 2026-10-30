#!/usr/bin/env node
"use strict";

// Distribution only: no archive operations, client configuration or LLM calls.
const fs = require("node:fs");
const fsp = require("node:fs/promises");
const path = require("node:path");
const crypto = require("node:crypto");
const { Readable, Transform } = require("node:stream");
const { pipeline } = require("node:stream/promises");

const MAX_BINARY_BYTES = 128 * 1024 * 1024;
const MAX_CHECKSUM_BYTES = 1024 * 1024;
const DOWNLOAD_TIMEOUT_MS = 60000;

function platformArch(platform = process.platform, arch = process.arch) {
  if (platform !== "darwin") throw new Error("betterzip-mcp supports macOS only.");
  if (arch === "x64") return "amd64";
  if (arch === "arm64") return "arm64";
  throw new Error(`Unsupported macOS architecture: ${arch}`);
}

function httpsURL(value) {
  const url = new URL(value);
  if (url.protocol !== "https:" || url.username || url.password) {
    throw new Error("Downloads require HTTPS without embedded credentials.");
  }
  return url;
}

async function responseFor(url, signal, fetcher = fetch) {
  let current = httpsURL(url);
  for (let redirects = 0; redirects <= 5; redirects += 1) {
    const response = await fetcher(current, { redirect: "manual", signal });
    if ([301, 302, 303, 307, 308].includes(response.status)) {
      const location = response.headers.get("location");
      if (response.body) await response.body.cancel();
      if (!location || redirects === 5) throw new Error("Invalid or excessive download redirects.");
      current = httpsURL(new URL(location, current));
      continue;
    }
    if (response.status !== 200 || !response.body) {
      if (response.body) await response.body.cancel();
      throw new Error(`Download failed: HTTP ${response.status} from ${current.hostname}`);
    }
    return response;
  }
  throw new Error("Download redirect limit exceeded.");
}

async function checksumText(url, signal, fetcher) {
  const response = await responseFor(url, signal, fetcher);
  const chunks = [];
  let size = 0;
  for await (const chunk of response.body) {
    size += chunk.length;
    if (size > MAX_CHECKSUM_BYTES) throw new Error("Checksum file exceeds 1 MiB.");
    chunks.push(Buffer.from(chunk));
  }
  return Buffer.concat(chunks).toString("utf8");
}

function expectedChecksum(text, filename) {
  const matches = [];
  for (const line of text.split(/\r?\n/)) {
    const match = /^([a-fA-F0-9]{64})\s+\*?(.+)$/.exec(line);
    if (match && match[2] === filename) matches.push(match[1].toLowerCase());
  }
  if (matches.length !== 1) throw new Error(`Expected one checksum entry for ${filename}.`);
  return matches[0];
}

// Validate the Mach-O header as well as its hash, to catch mislabeled releases.
function validateHeader(header, arch) {
  const expectedCPU = arch === "arm64" ? 0x0100000c : 0x01000007;
  if (header.length < 8 || header.readUInt32LE(0) !== 0xfeedfacf || header.readUInt32LE(4) !== expectedCPU) {
    throw new Error(`Release is not a 64-bit macOS ${arch} executable.`);
  }
}

async function install(options = {}) {
  const arch = platformArch(options.platform, options.arch);
  const packageRoot = options.packageRoot || __dirname;
  const pkg = JSON.parse(await fsp.readFile(path.join(packageRoot, "package.json"), "utf8"));
  if (!/^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/.test(pkg.version)) throw new Error("Invalid package version.");
  const config = pkg.betterzipConfig;
  if (!config?.urlPattern || !config?.checksumPattern) throw new Error("Missing release URL configuration.");
  const vars = { version: pkg.version, arch };
  const expand = (pattern) => pattern.replace(/\{(version|arch)\}/g, (_, key) => vars[key]);
  const filename = `betterzip-mcp-darwin-${arch}`;
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(new Error("Download exceeded 60 seconds.")), DOWNLOAD_TIMEOUT_MS);
  timer.unref();
  const binDir = path.join(packageRoot, "bin");
  const temporary = path.join(binDir, `.download-${crypto.randomBytes(12).toString("hex")}`);
  let created = false;
  try {
    const text = await checksumText(expand(config.checksumPattern), controller.signal, options.fetcher);
    const expected = expectedChecksum(text, filename);
    const response = await responseFor(expand(config.urlPattern), controller.signal, options.fetcher);
    await fsp.mkdir(binDir, { recursive: true });
    const stat = await fsp.lstat(binDir);
    if (!stat.isDirectory() || stat.isSymbolicLink()) {
      await response.body.cancel();
      throw new Error("The package bin directory must not be a symbolic link.");
    }
    const hash = crypto.createHash("sha256");
    let size = 0;
    let header = Buffer.alloc(0);
    const verifier = new Transform({
      transform(chunk, _encoding, callback) {
        size += chunk.length;
        if (size > MAX_BINARY_BYTES) { callback(new Error("Binary exceeds 128 MiB.")); return; }
        if (header.length < 8) header = Buffer.concat([header, chunk.subarray(0, 8 - header.length)]);
        hash.update(chunk);
        callback(null, chunk);
      },
    });
    const output = fs.createWriteStream(temporary, { flags: "wx", mode: 0o600 });
    output.once("open", () => { created = true; });
    await pipeline(Readable.fromWeb(response.body), verifier, output, { signal: controller.signal });
    if (hash.digest("hex") !== expected) throw new Error("Release SHA-256 checksum mismatch.");
    validateHeader(header, arch);
    await fsp.chmod(temporary, 0o755);
    await fsp.rename(temporary, path.join(binDir, "betterzip-mcp"));
    created = false;
    process.stderr.write(`betterzip-mcp: installed macOS ${arch} v${pkg.version}\n`);
    return path.join(binDir, "betterzip-mcp");
  } finally {
    clearTimeout(timer);
    controller.abort();
    if (created) await fsp.rm(temporary, { force: true });
  }
}

module.exports = { install, platformArch, expectedChecksum, responseFor, validateHeader };

if (require.main === module) {
  install().catch((error) => {
    process.stderr.write(`betterzip-mcp installation failed: ${error.message}\n`);
    process.exitCode = 1;
  });
}
