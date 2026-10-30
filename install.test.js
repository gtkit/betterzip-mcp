"use strict";

const { test } = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const crypto = require("node:crypto");
const { install, platformArch, expectedChecksum, responseFor, validateHeader } = require("./install.js");
const pkg = require("./package.json");

function binary(arch) {
  const value = Buffer.alloc(64);
  value.writeUInt32LE(0xfeedfacf, 0);
  value.writeUInt32LE(arch === "arm64" ? 0x0100000c : 0x01000007, 4);
  return value;
}

async function fixture(t) {
  const directory = await fs.mkdtemp(path.join(__dirname, ".test-install-"));
  t.after(() => fs.rm(directory, { force: true, recursive: true }));
  await fs.writeFile(path.join(directory, "package.json"), JSON.stringify(pkg));
  return directory;
}

function fetchFixture(bytes, arch = "amd64", digest) {
  const checksum = digest || crypto.createHash("sha256").update(bytes).digest("hex");
  return async (url) => String(url).endsWith("SHA256SUMS")
    ? new Response(`${checksum}  betterzip-mcp-darwin-${arch}\n`)
    : new Response(bytes);
}

test("architecture and checksum selection are exact", () => {
  assert.equal(platformArch("darwin", "x64"), "amd64");
  assert.equal(platformArch("darwin", "arm64"), "arm64");
  assert.throws(() => platformArch("linux", "x64"));
  assert.throws(() => platformArch("darwin", "ia32"));
  const hash = "f".repeat(64);
  assert.equal(expectedChecksum(`${hash} *binary\n`, "binary"), hash);
  assert.throws(() => expectedChecksum(`${hash}  binary-old\n`, "binary"));
  assert.throws(() => expectedChecksum(`${hash}  binary\n${hash}  binary\n`, "binary"));
  assert.throws(() => validateHeader(binary("arm64"), "amd64"));
});

test("download follows HTTPS redirects and rejects downgrade and HTTP errors", async () => {
  const calls = [];
  const response = await responseFor("https://example.test/file", undefined, async (url) => {
    calls.push(String(url));
    return calls.length === 1 ? new Response(null, { status: 302, headers: { location: "/asset" } }) : new Response("ok");
  });
  assert.equal(await response.text(), "ok");
  assert.equal(calls[1], "https://example.test/asset");
  await assert.rejects(responseFor("https://example.test/file", undefined, async () =>
    new Response(null, { status: 302, headers: { location: "http://example.test/asset" } })));
  await assert.rejects(responseFor("https://example.test/file", undefined, async () => new Response("no", { status: 404 })));
  let redirects = 0;
  await assert.rejects(responseFor("https://example.test/file", undefined, async () => {
    redirects += 1;
    return new Response(null, { status: 302, headers: { location: "/again" } });
  }));
  assert.equal(redirects, 6);
});

for (const arch of ["amd64", "arm64"]) {
  test(`installer verifies and installs ${arch}`, async (t) => {
    const directory = await fixture(t);
    const bytes = binary(arch);
    const filename = await install({ packageRoot: directory, platform: "darwin", arch: arch === "amd64" ? "x64" : arch, fetcher: fetchFixture(bytes, arch) });
    assert.deepEqual(await fs.readFile(filename), bytes);
    assert.equal((await fs.stat(filename)).mode & 0o777, 0o755);
    assert.deepEqual(await fs.readdir(path.join(directory, "bin")), ["betterzip-mcp"]);
  });
}

test("checksum failure preserves the installed binary and cleans temporary files", async (t) => {
  const directory = await fixture(t);
  await fs.mkdir(path.join(directory, "bin"));
  const oldBinary = path.join(directory, "bin", "betterzip-mcp");
  await fs.writeFile(oldBinary, "previous version");
  await assert.rejects(install({ packageRoot: directory, platform: "darwin", arch: "x64", fetcher: fetchFixture(binary("amd64"), "amd64", "0".repeat(64)) }));
  assert.equal(await fs.readFile(oldBinary, "utf8"), "previous version");
  assert.deepEqual(await fs.readdir(path.join(directory, "bin")), ["betterzip-mcp"]);
});

test("wrong architecture and missing checksum cannot install", async (t) => {
  const directory = await fixture(t);
  await assert.rejects(install({ packageRoot: directory, platform: "darwin", arch: "x64", fetcher: fetchFixture(binary("arm64")) }));
  assert.deepEqual(await fs.readdir(path.join(directory, "bin")), []);
  await assert.rejects(install({ packageRoot: directory, platform: "darwin", arch: "x64", fetcher: async () => new Response("not a checksum") }));
});

test("interrupted response removes partial download", async (t) => {
  const directory = await fixture(t);
  const fetcher = async (url) => {
    if (String(url).endsWith("SHA256SUMS")) return new Response(`${"0".repeat(64)}  betterzip-mcp-darwin-amd64\n`);
    let sent = false;
    return new Response(new ReadableStream({
      pull(controller) {
        if (!sent) { sent = true; controller.enqueue(binary("amd64")); }
        else controller.error(new Error("controlled connection reset"));
      },
    }));
  };
  await assert.rejects(install({ packageRoot: directory, platform: "darwin", arch: "x64", fetcher }));
  assert.deepEqual(await fs.readdir(path.join(directory, "bin")), []);
});

test("installer refuses a symlinked bin directory", async (t) => {
  const directory = await fixture(t);
  const target = path.join(directory, "other");
  await fs.mkdir(target);
  await fs.symlink(target, path.join(directory, "bin"));
  await assert.rejects(install({ packageRoot: directory, platform: "darwin", arch: "x64", fetcher: fetchFixture(binary("amd64")) }));
  assert.deepEqual(await fs.readdir(target), []);
});
