"use strict";

const { test } = require("node:test");
const assert = require("node:assert/strict");
const path = require("node:path");
const os = require("node:os");
const { serializeEvidence } = require("./codex-smoke-test.js");

test("saved evidence removes machine paths without changing live tool results", () => {
  const home = os.homedir();
  const project = path.join(home, 'projects', '资料 "归档"');
  const root = path.join(project, ".test-example", "data");
  const live = {
    codexHome: path.join(home, ".codex"),
    icon: path.join(project, "icon.png"),
    result: { content: [{ type: "text", text: `压缩完成：${root}/hello.zip` }] },
    success: true,
  };
  const saved = serializeEvidence(live, root, project, home);
  assert.ok(!saved.includes(home));
  assert.deepEqual(JSON.parse(saved), {
    codexHome: "${USER_HOME}/.codex",
    icon: "${PROJECT_ROOT}/icon.png",
    result: { content: [{ type: "text", text: "压缩完成：${TEST_ROOT}/hello.zip" }] },
    success: true,
  });
  assert.equal(live.result.content[0].text, `压缩完成：${root}/hello.zip`);
});
