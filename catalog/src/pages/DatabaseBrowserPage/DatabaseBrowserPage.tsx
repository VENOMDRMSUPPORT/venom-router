import { useCallback, useEffect, useRef, useState, type KeyboardEvent } from 'react';
import {
  LuCheck,
  LuChevronDown,
  LuChevronUp,
  LuClipboard,
  LuColumns3,
  LuDatabase,
  LuDownload,
  LuHistory,
  LuInfo,
  LuLoaderCircle,
  LuMaximize2,
  LuMinimize2,
  LuPlay,
  LuRefreshCw,
  LuTable2,
  LuTrash2,
  LuTriangleAlert,
  LuX,
} from 'react-icons/lu';
import {
  fetchDbQuery,
  fetchDbSchema,
  fetchDbTables,
  type DbQueryResponse,
  type DbTable,
  type DbValue,
  type DbSchema,
} from '../../api/client';
import styles from './DatabaseBrowserPage.module.css';

type QueryHistoryEntry = {
  id: string;
  sql: string;
  timestamp: string;
  success: boolean;
  rowCount?: number;
  error?: string;
};

const HISTORY_KEY = 'venom-catalog-db-history';
const MAX_HISTORY = 50;
let historySequence = 0;

function isAbortError(error: unknown): boolean {
  return error instanceof DOMException && error.name === 'AbortError'
    || error instanceof Error && error.name === 'AbortError';
}

function readHistory(): QueryHistoryEntry[] {
  if (typeof window === 'undefined') return [];
  try {
    const parsed = JSON.parse(window.localStorage.getItem(HISTORY_KEY) ?? '[]') as unknown;
    if (!Array.isArray(parsed)) return [];
    return parsed.filter((entry): entry is QueryHistoryEntry => {
      if (!entry || typeof entry !== 'object') return false;
      const item = entry as Partial<QueryHistoryEntry>;
      return typeof item.id === 'string'
        && typeof item.sql === 'string'
        && typeof item.timestamp === 'string'
        && typeof item.success === 'boolean';
    }).slice(-MAX_HISTORY);
  } catch {
    return [];
  }
}

function writeHistory(history: QueryHistoryEntry[]): boolean {
  if (typeof window === 'undefined') return false;
  try {
    window.localStorage.setItem(HISTORY_KEY, JSON.stringify(history.slice(-MAX_HISTORY)));
    return true;
  } catch {
    return false;
  }
}

function createHistoryId(): string {
  historySequence += 1;
  const randomId = globalThis.crypto?.randomUUID?.();
  return randomId ?? `${Date.now().toString(36)}-${historySequence.toString(36)}`;
}

function quoteIdentifier(identifier: string): string {
  return `"${identifier.replaceAll('"', '""')}"`;
}

function valueText(value: DbValue): string {
  if (value === null) return 'NULL';
  if (typeof value === 'string' || typeof value === 'number') return String(value);
  if (value.type === 'bigint') return `${value.value}n`;
  return `BLOB (${value.bytes} bytes) ${value.value}`;
}

function copyText(value: DbValue): string {
  if (value === null) return 'NULL';
  if (typeof value === 'string' || typeof value === 'number') return String(value);
  return value.value;
}

function queryFilename(): string {
  const stamp = new Date().toISOString().replace(/[.:]/g, '-');
  return `catalog-query-results-${stamp}.json`;
}

export function DatabaseBrowserPage() {
  const [tables, setTables] = useState<DbTable[]>([]);
  const [tablesLoading, setTablesLoading] = useState(true);
  const [tablesError, setTablesError] = useState<string | null>(null);
  const [selectedTable, setSelectedTable] = useState<string | null>(null);
  const [schema, setSchema] = useState<DbSchema | null>(null);
  const [schemaLoading, setSchemaLoading] = useState(false);
  const [schemaError, setSchemaError] = useState<string | null>(null);
  const [sql, setSql] = useState('');
  const [queryResult, setQueryResult] = useState<DbQueryResponse | null>(null);
  const [queryLoading, setQueryLoading] = useState(false);
  const [queryError, setQueryError] = useState<string | null>(null);
  const [history, setHistory] = useState<QueryHistoryEntry[]>([]);
  const [historyOpen, setHistoryOpen] = useState(false);
  const [schemaOpen, setSchemaOpen] = useState(true);
  const [resultsOpen, setResultsOpen] = useState(true);
  const [editorExpanded, setEditorExpanded] = useState(false);
  const [feedback, setFeedback] = useState<string | null>(null);
  const [statusMessage, setStatusMessage] = useState('Ready. Choose a table or enter a read-only query.');

  const editorRef = useRef<HTMLTextAreaElement>(null);
  const tablesControllerRef = useRef<AbortController | null>(null);
  const schemaControllerRef = useRef<AbortController | null>(null);
  const queryControllerRef = useRef<AbortController | null>(null);
  const queryGenerationRef = useRef(0);

  useEffect(() => {
    setHistory(readHistory());
  }, []);

  const loadTables = useCallback(async () => {
    tablesControllerRef.current?.abort();
    const controller = new AbortController();
    tablesControllerRef.current = controller;
    setTablesLoading(true);
    setTablesError(null);
    try {
      const result = await fetchDbTables(controller.signal);
      if (!controller.signal.aborted) {
        setTables(result.tables);
        setStatusMessage(`${result.tables.length} database tables loaded.`);
      }
    } catch (error) {
      if (!isAbortError(error) && !controller.signal.aborted) {
        setTablesError(error instanceof Error ? error.message : 'The database tables could not be loaded.');
        setStatusMessage('Loading database tables failed.');
      }
    } finally {
      if (!controller.signal.aborted) setTablesLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadTables();
    return () => tablesControllerRef.current?.abort();
  }, [loadTables]);

  const loadSchema = useCallback(async (tableName: string) => {
    schemaControllerRef.current?.abort();
    const controller = new AbortController();
    schemaControllerRef.current = controller;
    setSchema(null);
    setSchemaLoading(true);
    setSchemaError(null);
    try {
      const result = await fetchDbSchema(tableName, controller.signal);
      if (!controller.signal.aborted) setSchema(result);
    } catch (error) {
      if (!isAbortError(error) && !controller.signal.aborted) {
        setSchemaError(error instanceof Error ? error.message : 'The table schema could not be loaded.');
      }
    } finally {
      if (!controller.signal.aborted) setSchemaLoading(false);
    }
  }, []);

  useEffect(() => {
    if (selectedTable) void loadSchema(selectedTable);
    else {
      schemaControllerRef.current?.abort();
      setSchema(null);
    }
  }, [selectedTable, loadSchema]);

  useEffect(() => () => {
    tablesControllerRef.current?.abort();
    schemaControllerRef.current?.abort();
    queryControllerRef.current?.abort();
  }, []);

  const appendHistory = (entry: QueryHistoryEntry) => {
    const next = [...readHistory(), entry].slice(-MAX_HISTORY);
    if (!writeHistory(next)) setFeedback('History could not be saved in this browser.');
    setHistory(next);
  };

  const handleRunQuery = useCallback(async () => {
    const trimmed = sql.trim();
    if (!trimmed) return;

    queryControllerRef.current?.abort();
    const controller = new AbortController();
    queryControllerRef.current = controller;
    const generation = ++queryGenerationRef.current;
    setQueryLoading(true);
    setQueryError(null);
    setQueryResult(null);
    setFeedback(null);
    setStatusMessage('Running read-only query…');

    try {
      const result = await fetchDbQuery(trimmed, 1000, controller.signal);
      if (controller.signal.aborted || generation !== queryGenerationRef.current) return;
      setQueryResult(result);
      setStatusMessage(`${result.rowCount} row${result.rowCount === 1 ? '' : 's'} returned${result.truncated ? '; result truncated at the limit.' : '.'}`);
      appendHistory({
        id: createHistoryId(),
        sql: trimmed,
        timestamp: new Date().toISOString(),
        success: true,
        rowCount: result.rowCount,
      });
    } catch (error) {
      if (controller.signal.aborted || generation !== queryGenerationRef.current || isAbortError(error)) return;
      const message = error instanceof Error ? error.message : 'The read-only query could not be executed.';
      setQueryError(message);
      setStatusMessage('Query failed.');
      appendHistory({
        id: createHistoryId(),
        sql: trimmed,
        timestamp: new Date().toISOString(),
        success: false,
        error: message,
      });
    } finally {
      if (!controller.signal.aborted && generation === queryGenerationRef.current) setQueryLoading(false);
    }
  }, [queryLoading, sql]);

  const handleTableSelect = (tableName: string) => {
    setSelectedTable(tableName);
    setSql(`SELECT * FROM ${quoteIdentifier(tableName)} LIMIT 100;`);
    setQueryError(null);
    setStatusMessage(`Selected table ${tableName}.`);
  };

  const handleInsert = (text: string) => {
    const editor = editorRef.current;
    if (!editor) {
      setSql((current) => `${current}${current ? ' ' : ''}${text}`);
      return;
    }
    const start = editor.selectionStart;
    const end = editor.selectionEnd;
    setSql((current) => current.slice(0, start) + text + current.slice(end));
    requestAnimationFrame(() => {
      editor.focus();
      const position = start + text.length;
      editor.setSelectionRange(position, position);
    });
  };

  const copyToClipboard = async (text: string, label: string) => {
    try {
      if (!navigator.clipboard?.writeText) throw new Error('Clipboard unavailable');
      await navigator.clipboard.writeText(text);
      setFeedback(`${label} copied.`);
    } catch {
      setFeedback(`Could not copy ${label.toLowerCase()}.`);
    }
  };

  const handleExportResults = () => {
    if (!queryResult) return;
    try {
      const blob = new Blob([JSON.stringify(queryResult, null, 2)], { type: 'application/json;charset=utf-8' });
      const url = URL.createObjectURL(blob);
      const link = document.createElement('a');
      link.href = url;
      link.download = queryFilename();
      link.rel = 'noopener';
      document.body.appendChild(link);
      link.click();
      link.remove();
      URL.revokeObjectURL(url);
      setFeedback('Results exported.');
    } catch {
      setFeedback('Could not export results from this browser.');
    }
  };

  const handleClearHistory = () => {
    if (!writeHistory([])) setFeedback('History could not be cleared in this browser.');
    setHistory([]);
  };

  const handleClearResults = () => {
    setQueryResult(null);
    setQueryError(null);
    setStatusMessage('Results cleared.');
  };

  const handleCancelQuery = () => {
    queryGenerationRef.current += 1;
    queryControllerRef.current?.abort();
    setQueryLoading(false);
    setStatusMessage('Query cancelled.');
  };

  const handleEditorKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if ((event.ctrlKey || event.metaKey) && event.key === 'Enter') {
      event.preventDefault();
      void handleRunQuery();
    }
  };

  return (
    <div className={styles.page}>
      <div className={styles.intro}>
        <div>
          <p className={styles.eyebrow}><LuDatabase size={14} /> Read-only workspace</p>
          <p className={styles.description}>Inspect approved Catalog tables and run one bounded SELECT or read-only CTE at a time.</p>
        </div>
        <button
          type="button"
          className={styles.iconButton}
          onClick={() => void loadTables()}
          disabled={tablesLoading}
          aria-label="Refresh database tables"
          title="Refresh database tables"
        >
          <LuRefreshCw size={16} className={tablesLoading ? styles.spin : undefined} />
        </button>
      </div>

      <div className={styles.layout}>
        <aside className={`${styles.sidebar} ${schemaOpen ? '' : styles.sidebarCollapsed}`} aria-label="Database tables and schema">
          <div className={styles.panelHeader}>
            <div>
              <h2>Tables</h2>
              <span className={styles.panelHint}>{tablesLoading ? 'Loading…' : `${tables.length} available`}</span>
            </div>
            <button
              type="button"
              className={styles.iconButton}
              onClick={() => setSchemaOpen((open) => !open)}
              aria-label={schemaOpen ? 'Collapse table and schema panel' : 'Expand table and schema panel'}
              title={schemaOpen ? 'Collapse table and schema panel' : 'Expand table and schema panel'}
            >
              {schemaOpen ? <LuChevronUp size={16} /> : <LuChevronDown size={16} />}
            </button>
          </div>

          {tablesLoading ? (
            <div className={styles.loadingBlock}><LuLoaderCircle size={18} className={styles.spin} /> Loading tables…</div>
          ) : tablesError ? (
            <div className={styles.errorBlock} role="alert">
              <LuTriangleAlert size={16} />
              <span>{tablesError}</span>
              <button type="button" className={styles.textButton} onClick={() => void loadTables()}>Retry</button>
            </div>
          ) : (
            <ul className={styles.tableList} role="listbox" aria-label="Available Catalog tables">
              {tables.map((table) => (
                <li key={table.name} className={styles.tableItem}>
                  <button
                    type="button"
                    className={`${styles.tableButton} ${selectedTable === table.name ? styles.tableButtonActive : ''}`}
                    onClick={() => handleTableSelect(table.name)}
                    role="option"
                    aria-selected={selectedTable === table.name}
                  >
                    <LuTable2 size={15} />
                    <span className={styles.ellipsized} title={table.name}>{table.name}</span>
                  </button>
                  <button
                    type="button"
                    className={styles.rowIconButton}
                    onClick={() => void copyToClipboard(table.name, 'Table name')}
                    aria-label={`Copy table name ${table.name}`}
                    title="Copy table name"
                  >
                    <LuClipboard size={13} />
                  </button>
                </li>
              ))}
              {tables.length === 0 && <li className={styles.mutedBlock}>No tables are available.</li>}
            </ul>
          )}

          {schemaOpen && selectedTable && (
            <div className={styles.schemaPanel}>
              <div className={styles.panelHeaderCompact}>
                <div className={styles.schemaTitle}><LuColumns3 size={14} /><span className={styles.ellipsized} title={selectedTable}>{selectedTable}</span></div>
                <button
                  type="button"
                  className={styles.rowIconButton}
                  onClick={() => void copyToClipboard(JSON.stringify(schema, null, 2), 'Schema')}
                  disabled={!schema}
                  aria-label="Copy table schema"
                  title="Copy table schema as JSON"
                >
                  <LuClipboard size={13} />
                </button>
              </div>
              {schemaLoading && <div className={styles.loadingBlock}><LuLoaderCircle size={16} className={styles.spin} /> Loading schema…</div>}
              {schemaError && <div className={styles.errorBlock} role="alert"><LuTriangleAlert size={15} /><span>{schemaError}</span></div>}
              {schema && (
                <>
                  <div className={styles.schemaScroll}>
                    <table className={styles.schemaTable}>
                      <thead><tr><th>Column</th><th>Type</th><th>Required</th><th>Default</th></tr></thead>
                      <tbody>
                        {schema.columns.map((column) => (
                          <tr key={column.name}>
                            <td>
                              <div className={styles.nameWithAction}>
                                <button type="button" className={styles.inlineButton} onClick={() => handleInsert(quoteIdentifier(column.name))} title="Insert quoted column name into editor">{column.name}</button>
                                <button type="button" className={styles.rowIconButton} onClick={() => void copyToClipboard(column.name, 'Column name')} aria-label={`Copy column name ${column.name}`} title="Copy column name"><LuClipboard size={11} /></button>
                              </div>
                              {column.pk > 0 && <span className={styles.badge}>PK</span>}
                            </td>
                            <td className={styles.mono}>{column.type || '—'}</td>
                            <td>{column.notnull ? 'Yes' : 'No'}</td>
                            <td className={`${styles.mono} ${styles.longText}`} title={column.dflt_value ?? ''}>{column.dflt_value ?? '—'}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                  <SchemaList title="Indexes" items={schema.indexes.map((index) => `${index.name}${index.unique ? ' · UNIQUE' : ''}${index.partial ? ' · PARTIAL' : ''}`)} />
                  <SchemaList title="Foreign keys" items={schema.foreignKeys.map((key) => `${key.from} → ${key.table}.${key.to}`)} />
                </>
              )}
            </div>
          )}
        </aside>

        <main className={`${styles.main} ${editorExpanded ? styles.editorExpanded : ''}`}>
          <section className={styles.editorSection} aria-label="SQL editor">
            <div className={styles.sectionHeader}>
              <div>
                <h2>SQL editor</h2>
                <span className={styles.panelHint}>Ctrl+Enter or Cmd+Enter to run</span>
              </div>
              <div className={styles.buttonGroup}>
                <button type="button" className={styles.iconButton} onClick={() => setEditorExpanded((expanded) => !expanded)} aria-label={editorExpanded ? 'Minimize SQL editor' : 'Maximize SQL editor'} title={editorExpanded ? 'Minimize SQL editor' : 'Maximize SQL editor'}>{editorExpanded ? <LuMinimize2 size={15} /> : <LuMaximize2 size={15} />}</button>
                <button type="button" className={styles.iconButton} onClick={() => setSql('')} disabled={!sql} aria-label="Clear SQL editor" title="Clear SQL editor"><LuTrash2 size={15} /></button>
              </div>
            </div>
            <textarea
              ref={editorRef}
              className={styles.editor}
              value={sql}
              onChange={(event) => setSql(event.target.value)}
              onKeyDown={handleEditorKeyDown}
              placeholder={'SELECT * FROM "models" LIMIT 50;'}
              spellCheck={false}
              rows={editorExpanded ? 20 : 8}
              aria-label="Read-only SQL query"
            />
            <div className={styles.editorFooter}>
              <button type="button" className={styles.primaryButton} onClick={() => void handleRunQuery()} disabled={!sql.trim()}>
                {queryLoading ? <LuLoaderCircle size={15} className={styles.spin} /> : <LuPlay size={15} />}
                {queryLoading ? 'Running…' : 'Run query'}
              </button>
              {queryLoading && <button type="button" className={styles.secondaryButton} onClick={handleCancelQuery}><LuX size={15} /> Cancel</button>}
              <button type="button" className={styles.secondaryButton} onClick={handleExportResults} disabled={!queryResult} aria-label="Export query results" title="Export query results as JSON"><LuDownload size={15} /> Export</button>
              <button type="button" className={styles.secondaryButton} onClick={handleClearResults} disabled={!queryResult && !queryError} aria-label="Clear query results" title="Clear query results"><LuTrash2 size={15} /> Clear results</button>
              <button type="button" className={`${styles.secondaryButton} ${historyOpen ? styles.secondaryButtonActive : ''}`} onClick={() => setHistoryOpen((open) => !open)} aria-expanded={historyOpen}><LuHistory size={15} /> History ({history.length})</button>
            </div>

            {historyOpen && (
              <div className={styles.historyPanel} aria-label="Query history">
                <div className={styles.panelHeaderCompact}><span className={styles.panelLabel}>Recent queries</span><button type="button" className={styles.textButton} onClick={handleClearHistory} disabled={!history.length}>Clear history</button></div>
                {history.length === 0 ? <div className={styles.mutedBlock}>No queries recorded yet.</div> : (
                  <ul className={styles.historyList}>
                    {history.slice().reverse().map((entry) => (
                      <li key={entry.id} className={styles.historyItem}>
                        <button type="button" className={styles.historyQuery} onClick={() => { setSql(entry.sql); setHistoryOpen(false); }}>
                          <span className={styles.historyMeta}>{new Date(entry.timestamp).toLocaleString()} · {entry.success ? `${entry.rowCount ?? 0} rows` : 'Failed'}</span>
                          <code>{entry.sql}</code>
                        </button>
                        <button type="button" className={styles.rowIconButton} onClick={() => void copyToClipboard(entry.sql, 'Query')} aria-label="Copy query from history" title="Copy query"><LuClipboard size={13} /></button>
                      </li>
                    ))}
                  </ul>
                )}
              </div>
            )}
          </section>

          <section className={`${styles.resultsSection} ${resultsOpen ? '' : styles.resultsCollapsed}`} aria-label="Query results">
            <div className={styles.sectionHeader}>
              <div className={styles.titleWithIcon}><LuTable2 size={15} /><h2>Results</h2></div>
              <div className={styles.resultsHeaderRight}>
                {queryResult && <span className={styles.resultsMeta}>{queryResult.rowCount} row{queryResult.rowCount === 1 ? '' : 's'} returned{queryResult.truncated ? ` · limited to ${queryResult.limit}` : ''}</span>}
                <button type="button" className={styles.iconButton} onClick={() => setResultsOpen((open) => !open)} aria-label={resultsOpen ? 'Collapse results' : 'Expand results'} title={resultsOpen ? 'Collapse results' : 'Expand results'}>{resultsOpen ? <LuChevronUp size={16} /> : <LuChevronDown size={16} />}</button>
              </div>
            </div>
            {resultsOpen && (
              <>
                {queryLoading && <div className={styles.loadingBlock}><LuLoaderCircle size={18} className={styles.spin} /> Executing query…</div>}
                {queryError && <div className={styles.errorBanner} role="alert"><LuTriangleAlert size={17} /><span>{queryError}</span></div>}
                {queryResult && !queryLoading && <ResultsTable result={queryResult} onCopy={(value) => void copyToClipboard(copyText(value), 'Value')} />}
                {!queryResult && !queryError && !queryLoading && <div className={styles.emptyResults}><LuInfo size={24} /><span>Run a read-only query to see results.</span></div>}
              </>
            )}
          </section>
        </main>
      </div>

      <div className={styles.srStatus} aria-live="polite">{statusMessage}</div>
      {feedback && <div className={styles.feedback} role="status"><LuCheck size={15} /> {feedback}</div>}
    </div>
  );
}

function SchemaList({ title, items }: { title: string; items: string[] }) {
  if (!items.length) return null;
  return <div className={styles.schemaList}><h3>{title}</h3><ul>{items.map((item) => <li key={item} className={styles.longText} title={item}>{item}</li>)}</ul></div>;
}

function ResultsTable({ result, onCopy }: { result: DbQueryResponse; onCopy: (value: DbValue) => void }) {
  return (
    <div className={styles.resultsTableWrap}>
      <table className={styles.resultsTable}>
        <thead><tr><th className={styles.rowNumberHeader}>#</th>{result.columns.map((column, index) => <th key={`${column}-${index}`} title={column}>{column}</th>)}</tr></thead>
        <tbody>
          {result.rows.map((row, rowIndex) => (
            <tr key={rowIndex}>
              <td className={styles.rowNumber}>{rowIndex + 1}</td>
              {result.columns.map((column, columnIndex) => {
                const value = row.values[columnIndex] ?? null;
                return <td key={`${column}-${columnIndex}`}><div className={styles.resultCell}><span className={`${styles.resultValue} ${value === null ? styles.nullValue : ''}`} title={valueText(value)}>{valueText(value)}</span><button type="button" className={styles.rowIconButton} onClick={() => onCopy(value)} aria-label={`Copy value from row ${rowIndex + 1}, column ${column}`} title="Copy value"><LuClipboard size={12} /></button></div></td>;
              })}
            </tr>
          ))}
        </tbody>
      </table>
      {result.rows.length === 0 && <div className={styles.emptyResults}>Query returned no rows.</div>}
      {result.truncated && <div className={styles.truncatedNotice}><LuInfo size={15} /> Only the first {result.limit} rows are shown; the server stopped after reading the limit plus one row.</div>}
    </div>
  );
}
