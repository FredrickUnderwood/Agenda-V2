// Package @agenda/log browser: the client-side logger. It captures browser
// runtime logs (explicit calls plus, optionally, uncaught errors / rejected
// promises / console), buffers and batches them, and ships them with a
// fire-and-forget transport (sendBeacon on unload, else fetch keepalive) to an
// ingest endpoint — an app route backed by sdk/go/log/clientlog. It NEVER writes
// files (a browser can't); the server sink is what lands them in view-logs.

import {
  DEFAULTS,
  type LogEntry,
  type LogFields,
  type LogLevel,
  type LogPayload,
  TRACE_HEADER,
  levelEnabled,
  normalizeError,
  truncate,
} from "../core/index.js";

/** Minimal init accepted by a fetch-like transport. */
export interface FetchLikeInit {
  method?: string;
  body?: string;
  headers?: Record<string, string>;
  keepalive?: boolean;
  credentials?: string;
}

/**
 * The subset of `fetch` the transport needs, declared explicitly so the library
 * doesn't depend on whether the ambient `fetch` type comes from lib.dom or
 * @types/node (they differ) — and so tests can inject a stub.
 */
export type FetchLike = (input: string, init?: FetchLikeInit) => Promise<unknown>;

export interface BrowserLoggerOptions {
  /**
   * Where to POST batched logs. Use a same-origin path (e.g. "/api/client-logs")
   * so the browser's nginx reverse-proxies it to the backend and no CORS is
   * involved. The backend mounts clientlog.Handler there.
   */
  endpoint: string;
  /** Minimum level to emit. Default "info". */
  level?: LogLevel;
  /** Flush once this many entries are queued. Default 20. */
  batchSize?: number;
  /** Flush at least this often (ms). Default 5000. 0 disables the timer. */
  flushIntervalMs?: number;
  /** Hard cap on queued entries; oldest dropped past this. Default 1000. */
  maxQueue?: number;
  /** Truncate each msg to this many chars. Default 4096. */
  maxMessageLen?: number;
  /**
   * Fields merged into every entry (under the client payload) — e.g. app /
   * release / build so client logs are attributable even though the server line
   * carries the backend's own identity.
   */
  baseFields?: LogFields;
  /**
   * Returns the current trace id (e.g. read from a <meta> the backend rendered).
   * When it yields a value, it is sent as the X-Agenda-Trace-Id header so the
   * ingested logs correlate to the backend request chain.
   */
  getTraceId?: () => string | undefined;
  /** Last-chance hook to mutate or drop (return null) an entry before queueing. */
  beforeSend?: (entry: LogEntry) => LogEntry | null;
  /** Capture uncaught errors via window.onerror. Default true. */
  captureGlobalErrors?: boolean;
  /** Capture unhandled promise rejections. Default true. */
  captureUnhandledRejections?: boolean;
  /**
   * Mirror console output. true captures error+warn; pass an explicit list to
   * choose. Default false (explicit logging only) to avoid noise/recursion.
   */
  captureConsole?: boolean | Array<"error" | "warn">;
  /** Injected for tests / non-standard runtimes. Defaults to global fetch. */
  fetchImpl?: FetchLike;
  /** Injected for tests. Defaults to () => new Date(). */
  now?: () => Date;
}

/** Returned by install(); call it to remove listeners and stop the timer. */
export type Uninstall = () => void;

const isBrowser = typeof window !== "undefined";

export class BrowserLogger {
  private readonly endpoint: string;
  private readonly level: LogLevel;
  private readonly batchSize: number;
  private readonly flushIntervalMs: number;
  private readonly maxQueue: number;
  private readonly maxMessageLen: number;
  private readonly baseFields?: LogFields;
  private readonly getTraceId?: () => string | undefined;
  private readonly beforeSend?: (entry: LogEntry) => LogEntry | null;
  private readonly fetchImpl?: FetchLike;
  private readonly now: () => Date;

  private queue: LogEntry[] = [];
  private timer: ReturnType<typeof setInterval> | undefined;
  private installed = false;
  private teardown: Array<() => void> = [];

  constructor(opts: BrowserLoggerOptions) {
    if (!opts.endpoint) throw new Error("BrowserLogger: endpoint is required");
    this.endpoint = opts.endpoint;
    this.level = opts.level ?? DEFAULTS.level;
    this.batchSize = opts.batchSize ?? DEFAULTS.batchSize;
    this.flushIntervalMs = opts.flushIntervalMs ?? DEFAULTS.flushIntervalMs;
    this.maxQueue = opts.maxQueue ?? DEFAULTS.maxQueue;
    this.maxMessageLen = opts.maxMessageLen ?? DEFAULTS.maxMessageLen;
    this.baseFields = opts.baseFields;
    this.getTraceId = opts.getTraceId;
    this.beforeSend = opts.beforeSend;
    this.fetchImpl =
      opts.fetchImpl ?? (isBrowser ? (window.fetch.bind(window) as unknown as FetchLike) : undefined);
    this.now = opts.now ?? (() => new Date());

    this._captureConsole = opts.captureConsole ?? false;
    this._captureGlobalErrors = opts.captureGlobalErrors ?? true;
    this._captureUnhandledRejections = opts.captureUnhandledRejections ?? true;
  }

  private readonly _captureConsole: boolean | Array<"error" | "warn">;
  private readonly _captureGlobalErrors: boolean;
  private readonly _captureUnhandledRejections: boolean;

  debug(msg: string, fields?: LogFields): void {
    this.emit("debug", msg, fields);
  }
  info(msg: string, fields?: LogFields): void {
    this.emit("info", msg, fields);
  }
  warn(msg: string, fields?: LogFields): void {
    this.emit("warn", msg, fields);
  }
  error(msg: string, fields?: LogFields): void {
    this.emit("error", msg, fields);
  }

  /** Logs a caught error, attaching its message and stack. */
  captureError(err: unknown, fields?: LogFields): void {
    const { msg, stack } = normalizeError(err);
    this.emit("error", msg, { ...fields, ...(stack ? { stack } : {}) }, "exception");
  }

  private emit(level: LogLevel, msg: string, fields?: LogFields, logger?: string): void {
    if (!levelEnabled(this.level, level)) return;
    let entry: LogEntry = {
      level,
      msg: truncate(String(msg), this.maxMessageLen),
      ts: this.now().toISOString(),
      ...(logger ? { logger } : {}),
      fields: this.mergeFields(fields),
    };
    if (this.beforeSend) {
      const out = this.beforeSend(entry);
      if (!out) return;
      entry = out;
    }
    this.enqueue(entry);
  }

  private mergeFields(fields?: LogFields): LogFields | undefined {
    const merged = { ...this.baseFields, ...fields };
    return Object.keys(merged).length > 0 ? merged : undefined;
  }

  private enqueue(entry: LogEntry): void {
    this.queue.push(entry);
    if (this.queue.length > this.maxQueue) {
      // Drop oldest to bound memory under a burst / dead endpoint.
      this.queue.splice(0, this.queue.length - this.maxQueue);
    }
    if (this.queue.length >= this.batchSize) {
      void this.flush();
    }
  }

  /**
   * Sends all queued entries now. `useBeacon` uses navigator.sendBeacon (for
   * page-unload, where fetch may be cancelled). Resolves once dispatched; never
   * rejects — a failed send drops that batch rather than growing memory.
   */
  async flush(useBeacon = false): Promise<void> {
    if (this.queue.length === 0) return;
    const batch = this.queue;
    this.queue = [];
    const payload: LogPayload = { logs: batch };
    const body = JSON.stringify(payload);
    const traceId = this.getTraceId?.();

    if (useBeacon && isBrowser && typeof navigator !== "undefined" && navigator.sendBeacon) {
      try {
        const blob = new Blob([body], { type: "application/json" });
        if (navigator.sendBeacon(this.endpoint, blob)) return;
      } catch {
        // fall through to fetch
      }
    }

    const doFetch = this.fetchImpl;
    if (!doFetch) return;
    const headers: Record<string, string> = { "Content-Type": "application/json" };
    if (traceId) headers[TRACE_HEADER] = traceId;
    try {
      await doFetch(this.endpoint, { method: "POST", body, headers, keepalive: true, credentials: "same-origin" });
    } catch {
      // Endpoint down / offline: this batch is dropped on purpose.
    }
  }

  /**
   * Wires up the flush timer and the opted-in global/console captures, plus a
   * best-effort final flush on page hide. Idempotent; returns an uninstall fn.
   * A no-op (returns a no-op) outside a browser, so it is SSR-safe to call.
   */
  install(): Uninstall {
    if (this.installed || !isBrowser) return () => {};
    this.installed = true;

    if (this.flushIntervalMs > 0) {
      this.timer = setInterval(() => void this.flush(), this.flushIntervalMs);
      this.teardown.push(() => {
        if (this.timer) clearInterval(this.timer);
        this.timer = undefined;
      });
    }

    if (this._captureGlobalErrors) {
      const onError = (event: ErrorEvent) => {
        const { msg, stack } = normalizeError(event.error ?? event.message);
        this.emit("error", msg, this.locationFields({ stack, filename: event.filename, line: event.lineno, col: event.colno }), "window.onerror");
      };
      window.addEventListener("error", onError, true);
      this.teardown.push(() => window.removeEventListener("error", onError, true));
    }

    if (this._captureUnhandledRejections) {
      const onRejection = (event: PromiseRejectionEvent) => {
        const { msg, stack } = normalizeError(event.reason);
        this.emit("error", msg, this.locationFields({ stack }), "unhandledrejection");
      };
      window.addEventListener("unhandledrejection", onRejection);
      this.teardown.push(() => window.removeEventListener("unhandledrejection", onRejection));
    }

    const consoleLevels = this._captureConsole === true ? (["error", "warn"] as const) : this._captureConsole;
    if (Array.isArray(consoleLevels)) {
      for (const method of consoleLevels) {
        const original = console[method] as (...a: unknown[]) => void;
        const wrapped = (...args: unknown[]) => {
          this.emit(method === "warn" ? "warn" : "error", args.map(stringifyArg).join(" "), undefined, "console");
          original(...args);
        };
        console[method] = wrapped as typeof console.error;
        this.teardown.push(() => {
          console[method] = original as typeof console.error;
        });
      }
    }

    const onHide = () => void this.flush(true);
    window.addEventListener("pagehide", onHide);
    const onVisibility = () => {
      if (document.visibilityState === "hidden") void this.flush(true);
    };
    document.addEventListener("visibilitychange", onVisibility);
    this.teardown.push(() => {
      window.removeEventListener("pagehide", onHide);
      document.removeEventListener("visibilitychange", onVisibility);
    });

    return () => this.dispose();
  }

  /** Removes everything install() set up and flushes a final batch. */
  dispose(): void {
    for (const fn of this.teardown.splice(0)) fn();
    this.installed = false;
    void this.flush(true);
  }

  private locationFields(extra: LogFields): LogFields {
    const base: LogFields = {};
    if (isBrowser) {
      base.url = location.href;
      base.userAgent = navigator.userAgent;
    }
    for (const [k, v] of Object.entries(extra)) {
      if (v !== undefined && v !== null && v !== "") base[k] = v;
    }
    return base;
  }
}

function stringifyArg(arg: unknown): string {
  if (typeof arg === "string") return arg;
  if (arg instanceof Error) return arg.stack ?? arg.message;
  try {
    return JSON.stringify(arg);
  } catch {
    return String(arg);
  }
}

/** Convenience: construct a BrowserLogger and install its captures in one call. */
export function createBrowserLogger(opts: BrowserLoggerOptions): { logger: BrowserLogger; uninstall: Uninstall } {
  const logger = new BrowserLogger(opts);
  const uninstall = logger.install();
  return { logger, uninstall };
}
