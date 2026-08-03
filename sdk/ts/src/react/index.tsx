// Package @agenda/log react: thin React bindings over the browser logger — a
// provider so components share one logger, a hook to reach it, and an error
// boundary that reports render-time crashes (which window.onerror never sees).
// React is a peer dependency; only apps importing this entry take it on.

import * as React from "react";

import { BrowserLogger } from "../browser/index.js";
import type { LogFields } from "../core/index.js";

const LoggerContext = React.createContext<BrowserLogger | null>(null);

export interface AgendaLogProviderProps {
  logger: BrowserLogger;
  children: React.ReactNode;
}

/** Makes `logger` available to descendants via useLogger / AgendaErrorBoundary. */
export function AgendaLogProvider({ logger, children }: AgendaLogProviderProps): React.ReactElement {
  return <LoggerContext.Provider value={logger}>{children}</LoggerContext.Provider>;
}

/** Returns the provided BrowserLogger. Throws if no AgendaLogProvider is above. */
export function useLogger(): BrowserLogger {
  const logger = React.useContext(LoggerContext);
  if (!logger) {
    throw new Error("useLogger must be used within an <AgendaLogProvider>");
  }
  return logger;
}

export interface AgendaErrorBoundaryProps {
  children: React.ReactNode;
  /** Rendered when a child throws. A function receives the error. */
  fallback?: React.ReactNode | ((error: Error) => React.ReactNode);
  /** Explicit logger; falls back to the nearest AgendaLogProvider. */
  logger?: BrowserLogger;
  /** Extra fields to attach to the reported error. */
  fields?: LogFields;
  /** Called after logging, e.g. to reset app state or notify. */
  onError?: (error: Error, info: React.ErrorInfo) => void;
}

interface AgendaErrorBoundaryState {
  error: Error | null;
}

/**
 * Catches render/lifecycle errors in its subtree, reports them through the
 * logger (with the React component stack, which window.onerror lacks), and
 * renders `fallback`. Class component because error boundaries have no hook form.
 */
export class AgendaErrorBoundary extends React.Component<AgendaErrorBoundaryProps, AgendaErrorBoundaryState> {
  static override contextType = LoggerContext;

  override state: AgendaErrorBoundaryState = { error: null };

  static getDerivedStateFromError(error: Error): AgendaErrorBoundaryState {
    return { error };
  }

  override componentDidCatch(error: Error, info: React.ErrorInfo): void {
    const logger = this.props.logger ?? (this.context as BrowserLogger | null);
    logger?.error(error.message || "React render error", {
      ...this.props.fields,
      source: "react-error-boundary",
      stack: error.stack,
      componentStack: info.componentStack,
    });
    this.props.onError?.(error, info);
  }

  override render(): React.ReactNode {
    const { error } = this.state;
    if (error) {
      const { fallback } = this.props;
      if (typeof fallback === "function") return fallback(error);
      return fallback ?? null;
    }
    return this.props.children;
  }
}
