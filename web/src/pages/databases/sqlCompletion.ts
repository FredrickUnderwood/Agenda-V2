import { ifNotIn } from '@codemirror/autocomplete'
import type { Completion, CompletionContext, CompletionResult, CompletionSource } from '@codemirror/autocomplete'
import type { SQLNamespace } from '@codemirror/lang-sql'

// @codemirror/lang-sql already resolves qualified names — `orders.` and, through
// its alias table, `o.` — but a bare column name only completes when its schema
// config also names a `defaultTable`, which is static and so cannot follow the
// FROM clause the user is actually writing. That leaves the most common case
// uncovered: `SELECT * FROM orders WHERE cus…`.
//
// This source fills exactly that gap: it offers the columns of whichever tables
// the statement's FROM/JOIN clauses mention, unqualified, and defers to lang-sql
// everywhere else.

// Words that may follow a table name without being an alias, so `FROM orders
// WHERE ...` does not read WHERE as an alias for orders.
const NOT_AN_ALIAS = new Set([
  'where',
  'group',
  'order',
  'having',
  'limit',
  'offset',
  'union',
  'inner',
  'left',
  'right',
  'full',
  'cross',
  'natural',
  'join',
  'on',
  'using',
  'set',
  'as',
  'and',
  'or',
  'for',
  'into',
  'straight_join',
  'partition',
  'window',
  'fetch',
])

// Matches FROM/JOIN followed by a table name and an optional alias. A schema
// qualifier is matched but not captured — the schema map is per-schema, so only
// the table part is ever looked up. Capturing the two parts separately (rather
// than splitting the match on a dot afterwards) is what keeps backtick quoting
// on either side from being mis-stripped.
const IDENT = String.raw`(?:\x60[^\x60]+\x60|[\w$]+)`
const FROM_CLAUSE = new RegExp(
  String.raw`\b(?:from|join)\s+(?:${IDENT}\.)?(${IDENT})(?:\s+(?:as\s+)?(${IDENT}))?`,
  'gi',
)

export interface TableRef {
  /** The table name, as it is keyed in the schema map. */
  table: string
  /** The alias, when the statement gave one. */
  alias?: string
}

function unquote(name: string): string {
  const q = '`'
  return name.startsWith(q) && name.endsWith(q) ? name.slice(1, -1) : name
}

/**
 * Extracts the tables a statement reads from, with their aliases.
 *
 * A regex rather than the syntax tree, because the text is usually mid-edit and
 * unparseable at the moment completion is wanted — which is the whole point.
 */
export function tablesInScope(statement: string): TableRef[] {
  const refs: TableRef[] = []
  const seen = new Set<string>()

  for (const match of statement.matchAll(FROM_CLAUSE)) {
    const table = unquote(match[1])

    let alias: string | undefined
    if (match[2]) {
      const candidate = unquote(match[2])
      if (!NOT_AN_ALIAS.has(candidate.toLowerCase())) alias = candidate
    }

    const key = `${table} ${alias ?? ''}`
    if (seen.has(key)) continue
    seen.add(key)
    refs.push({ table, alias })
  }
  return refs
}

function columnsOf(schema: SQLNamespace, table: string): string[] {
  const entry = (schema as Record<string, unknown>)[table]
  if (!Array.isArray(entry)) return []
  return entry.filter((c): c is string => typeof c === 'string')
}

/**
 * A completion source offering the unqualified columns of a statement's own
 * tables. Returns null wherever lang-sql is the better authority.
 */
export function unqualifiedColumnSource(schema: SQLNamespace | undefined): CompletionSource {
  const source = (context: CompletionContext): CompletionResult | null => {
    if (!schema) return null

    const word = context.matchBefore(/[\w$]*/)
    if (!word || (word.from === word.to && !context.explicit)) return null

    // After a dot the name is qualified and lang-sql resolves it, aliases
    // included. Offering bare columns there would be wrong.
    if (context.state.sliceDoc(Math.max(0, word.from - 1), word.from) === '.') return null

    const refs = tablesInScope(context.state.doc.toString())
    if (refs.length === 0) return null

    const options: Completion[] = []
    for (const ref of refs) {
      for (const column of columnsOf(schema, ref.table)) {
        options.push({
          label: column,
          type: 'property',
          // With more than one table in scope the same column name can come
          // from either, so say which one this is.
          detail: ref.alias ?? ref.table,
          boost: 1,
        })
      }
    }
    if (options.length === 0) return null

    return { from: word.from, options, validFor: /^[\w$]*$/ }
  }

  // The same exclusions lang-sql applies to its own source: a column name is
  // not wanted inside a string literal or a comment.
  return ifNotIn(['QuotedIdentifier', 'SpecialVar', 'String', 'LineComment', 'BlockComment'], source)
}
