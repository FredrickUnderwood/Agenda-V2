import { useMemo, useRef } from 'react'
import CodeMirror, { EditorView, Prec, keymap } from '@uiw/react-codemirror'
import { MySQL, sql } from '@codemirror/lang-sql'
import type { SQLNamespace } from '@codemirror/lang-sql'
import { unqualifiedColumnSource } from './sqlCompletion'

export function SqlEditor({
  value,
  onChange,
  onRun,
  schema,
  height = '240px',
}: {
  value: string
  onChange: (next: string) => void
  onRun: () => void
  // Table → columns. Drives table and column completion; keyword completion
  // works without it.
  schema?: SQLNamespace
  height?: string
}) {
  // The run handler is held in a ref so the keymap extension is built from the
  // schema alone. Rebuilding extensions on every keystroke (which is what
  // depending on onRun would cause) tears down and re-creates the editor's
  // state, losing the cursor.
  const runRef = useRef(onRun)
  runRef.current = onRun

  const extensions = useMemo(() => {
    const base = sql({ dialect: MySQL, schema, upperCaseKeywords: true })
    return [
      base,
      // Registered as a second source for the SQL language rather than
      // replacing lang-sql's: it only covers unqualified columns, and
      // everything else — keywords, tables, qualified names — stays with the
      // source that already does it properly.
      base.language.data.of({ autocomplete: unqualifiedColumnSource(schema) }),
      // Highest precedence so it wins over the completion popup's own Enter
      // handling: Cmd/Ctrl+Enter should run the query even mid-completion.
      Prec.highest(
        keymap.of([
          {
            key: 'Mod-Enter',
            run: () => {
              runRef.current()
              return true
            },
          },
        ]),
      ),
      EditorView.lineWrapping,
    ]
  }, [schema])

  return (
    <CodeMirror
      value={value}
      onChange={onChange}
      height={height}
      extensions={extensions}
      placeholder="SELECT … — read-only statements only"
      basicSetup={{
        lineNumbers: true,
        foldGutter: false,
        autocompletion: true,
        highlightActiveLine: true,
        highlightActiveLineGutter: true,
      }}
      style={{ border: '1px solid #d9d9d9', borderRadius: 6, overflow: 'hidden', fontSize: 13 }}
    />
  )
}
