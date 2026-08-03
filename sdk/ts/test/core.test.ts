import { test } from "node:test";
import assert from "node:assert/strict";

import { levelEnabled, normalizeLevel, normalizeError, truncate } from "../src/core/index.js";

test("levelEnabled respects severity ordering", () => {
  assert.equal(levelEnabled("info", "error"), true);
  assert.equal(levelEnabled("info", "warn"), true);
  assert.equal(levelEnabled("info", "info"), true);
  assert.equal(levelEnabled("info", "debug"), false);
  assert.equal(levelEnabled("error", "warn"), false);
  assert.equal(levelEnabled("debug", "debug"), true);
});

test("normalizeLevel folds aliases and defaults to info", () => {
  assert.equal(normalizeLevel("WARN"), "warn");
  assert.equal(normalizeLevel("warning"), "warn");
  assert.equal(normalizeLevel("fatal"), "error");
  assert.equal(normalizeLevel("trace"), "debug");
  assert.equal(normalizeLevel("nonsense"), "info");
});

test("truncate cuts and marks long strings only", () => {
  assert.equal(truncate("short", 10), "short");
  assert.equal(truncate("abcdef", 3), "abc…");
  assert.equal(truncate("abc", 0), "abc"); // non-positive max is a no-op
});

test("normalizeError extracts message and stack", () => {
  const e = new Error("boom");
  const out = normalizeError(e);
  assert.equal(out.msg, "boom");
  assert.ok(out.stack && out.stack.includes("boom"));

  assert.deepEqual(normalizeError("plain"), { msg: "plain" });
  assert.deepEqual(normalizeError({ code: 42 }), { msg: '{"code":42}' });
});
