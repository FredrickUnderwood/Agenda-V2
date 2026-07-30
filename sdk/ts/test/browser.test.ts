import { test } from "node:test";
import assert from "node:assert/strict";

import { BrowserLogger, type FetchLikeInit } from "../src/browser/index.js";
import type { LogPayload } from "../src/core/index.js";

interface Sent {
  input: string;
  init?: FetchLikeInit;
}

// makeLogger builds a BrowserLogger with a stub transport that records every
// POST, so the batching/level/transport logic is exercised without a DOM.
function makeLogger(overrides: Record<string, unknown> = {}) {
  const sent: Sent[] = [];
  const logger = new BrowserLogger({
    endpoint: "/api/client-logs",
    flushIntervalMs: 0, // no timer under the test runner
    now: () => new Date("2026-07-30T00:00:00.000Z"),
    fetchImpl: (input, init) => {
      sent.push({ input, init });
      return Promise.resolve(undefined);
    },
    ...overrides,
  });
  return { logger, sent };
}

function decode(sent: Sent[]): LogPayload[] {
  return sent.map((s) => JSON.parse(String(s.init?.body)) as LogPayload);
}

test("buffers until flush, then POSTs one batch to the endpoint", async () => {
  const { logger, sent } = makeLogger();
  logger.info("hello", { a: 1 });
  logger.warn("careful");
  assert.equal(sent.length, 0, "nothing sent before flush / batch threshold");

  await logger.flush();
  assert.equal(sent.length, 1);
  assert.equal(sent[0]!.input, "/api/client-logs");
  assert.equal(sent[0]!.init?.method, "POST");

  const [payload] = decode(sent);
  assert.equal(payload!.logs.length, 2);
  assert.equal(payload!.logs[0]!.msg, "hello");
  assert.equal(payload!.logs[0]!.level, "info");
  assert.deepEqual(payload!.logs[0]!.fields, { a: 1 });
  assert.equal(payload!.logs[0]!.ts, "2026-07-30T00:00:00.000Z");
});

test("auto-flushes once batchSize is reached", async () => {
  const { logger, sent } = makeLogger({ batchSize: 2 });
  logger.info("one");
  assert.equal(sent.length, 0);
  logger.info("two"); // hits batchSize -> auto flush
  // enqueue() calls flush() synchronously; its fetch resolves on a microtask.
  await Promise.resolve();
  assert.equal(sent.length, 1);
  assert.equal(decode(sent)[0]!.logs.length, 2);
});

test("drops entries below the minimum level", async () => {
  const { logger, sent } = makeLogger({ level: "warn" });
  logger.info("ignored");
  logger.debug("ignored too");
  logger.error("kept");
  await logger.flush();
  const [payload] = decode(sent);
  assert.equal(payload!.logs.length, 1);
  assert.equal(payload!.logs[0]!.msg, "kept");
});

test("merges baseFields into every entry", async () => {
  const { logger, sent } = makeLogger({ baseFields: { app: "web", release: "abc123" } });
  logger.error("x", { extra: true });
  await logger.flush();
  const [payload] = decode(sent);
  assert.deepEqual(payload!.logs[0]!.fields, { app: "web", release: "abc123", extra: true });
});

test("sends the trace header when getTraceId yields one", async () => {
  const { logger, sent } = makeLogger({ getTraceId: () => "trace-xyz" });
  logger.error("x");
  await logger.flush();
  assert.equal(sent[0]!.init?.headers?.["X-Agenda-Trace-Id"], "trace-xyz");
});

test("beforeSend can drop entries", async () => {
  const { logger, sent } = makeLogger({
    beforeSend: (e: { msg: string }) => (e.msg === "secret" ? null : e),
  });
  logger.error("secret");
  logger.error("ok");
  await logger.flush();
  const [payload] = decode(sent);
  assert.equal(payload!.logs.length, 1);
  assert.equal(payload!.logs[0]!.msg, "ok");
});

test("bounds the queue to maxQueue, dropping oldest", async () => {
  const { logger, sent } = makeLogger({ maxQueue: 2, batchSize: 1000 });
  logger.info("a");
  logger.info("b");
  logger.info("c");
  await logger.flush();
  const [payload] = decode(sent);
  assert.deepEqual(
    payload!.logs.map((l) => l.msg),
    ["b", "c"],
  );
});

test("captureError attaches the stack", async () => {
  const { logger, sent } = makeLogger();
  logger.captureError(new Error("kaboom"), { where: "checkout" });
  await logger.flush();
  const [payload] = decode(sent);
  const entry = payload!.logs[0]!;
  assert.equal(entry.level, "error");
  assert.equal(entry.msg, "kaboom");
  assert.equal(entry.logger, "exception");
  assert.equal(entry.fields?.where, "checkout");
  assert.ok(String(entry.fields?.stack).includes("kaboom"));
});
