// Package @agenda/log core: the isomorphic, dependency-free pieces shared by the
// browser client and any server-side sink — the log shape, level handling, and
// the wire contract the browser transport POSTs and the Go ingest
// (sdk/go/log/clientlog) reads. Importing this entry pulls in no DOM or Node
// APIs, so it is safe from any runtime.

/** The four levels, ordered least→most severe, matching sdk/go/log. */
export type LogLevel = "debug" | "info" | "warn" | "error";

/** All levels in severity order. */
export const LOG_LEVELS: readonly LogLevel[] = ["debug", "info", "warn", "error"];

const LEVEL_PRIORITY: Record<LogLevel, number> = {
  debug: 10,
  info: 20,
  warn: 30,
  error: 40,
};

/** Reports whether an entry at `level` should be emitted given minimum `min`. */
export function levelEnabled(min: LogLevel, level: LogLevel): boolean {
  return LEVEL_PRIORITY[level] >= LEVEL_PRIORITY[min];
}

/** Arbitrary structured context attached to a log entry. */
export type LogFields = Record<string, unknown>;

/**
 * One log line. `ts` is the client's ISO-8601 timestamp; the server sink stamps
 * its own receive time as the authoritative line time and keeps this under a
 * `client_ts` field, so a skewed client clock never rewrites history.
 */
export interface LogEntry {
  level: LogLevel;
  msg: string;
  ts: string;
  /** Originating source, e.g. "console", "window.onerror", "react". */
  logger?: string;
  fields?: LogFields;
}

/** The POST body shape: `{"logs":[ ... ]}` — matches clientlog.wirePayload. */
export interface LogPayload {
  logs: LogEntry[];
}

/**
 * The trace header the agenda gateway sets and services propagate. The browser
 * transport echoes it (when a trace id is available) so an ingested client log
 * correlates to the same request chain as the backend that received it.
 * Mirrors log.TraceHeader in sdk/go/log.
 */
export const TRACE_HEADER = "X-Agenda-Trace-Id";

/** Shared defaults, referenced by the browser logger and documented in README. */
export const DEFAULTS = {
  level: "info" as LogLevel,
  /** flush once this many entries are queued */
  batchSize: 20,
  /** flush at least this often (ms) */
  flushIntervalMs: 5000,
  /** hard cap on queued entries; oldest are dropped past this */
  maxQueue: 1000,
  /** truncate each msg to this many chars (server also caps) */
  maxMessageLen: 4096,
} as const;

/** Clips s to at most max chars, appending an ellipsis when it had to cut. */
export function truncate(s: string, max: number = DEFAULTS.maxMessageLen): string {
  if (max <= 0 || s.length <= max) return s;
  return s.slice(0, max) + "…";
}

/** Coerces any level-ish string to a valid LogLevel, folding common aliases. */
export function normalizeLevel(level: string): LogLevel {
  switch (level.trim().toLowerCase()) {
    case "debug":
    case "trace":
      return "debug";
    case "warn":
    case "warning":
      return "warn";
    case "error":
    case "fatal":
    case "critical":
      return "error";
    default:
      return "info";
  }
}

/** Extracts a message and (when present) a stack from anything thrown. */
export function normalizeError(err: unknown): { msg: string; stack?: string } {
  if (err instanceof Error) {
    return { msg: err.message || err.name || "Error", stack: err.stack };
  }
  if (typeof err === "string") return { msg: err };
  try {
    return { msg: JSON.stringify(err) };
  } catch {
    return { msg: String(err) };
  }
}
