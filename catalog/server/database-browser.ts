import { constants } from 'node:sqlite';
import type { Db } from '../db/index.ts';

const {
  SQLITE_ALTER_TABLE,
  SQLITE_ANALYZE,
  SQLITE_ATTACH,
  SQLITE_CREATE_INDEX,
  SQLITE_CREATE_TABLE,
  SQLITE_CREATE_TEMP_INDEX,
  SQLITE_CREATE_TEMP_TABLE,
  SQLITE_CREATE_TEMP_TRIGGER,
  SQLITE_CREATE_TEMP_VIEW,
  SQLITE_CREATE_TRIGGER,
  SQLITE_CREATE_VIEW,
  SQLITE_CREATE_VTABLE,
  SQLITE_DELETE,
  SQLITE_DETACH,
  SQLITE_DROP_INDEX,
  SQLITE_DROP_TABLE,
  SQLITE_DROP_TEMP_INDEX,
  SQLITE_DROP_TEMP_TABLE,
  SQLITE_DROP_TEMP_TRIGGER,
  SQLITE_DROP_TEMP_VIEW,
  SQLITE_DROP_TRIGGER,
  SQLITE_DROP_VIEW,
  SQLITE_DROP_VTABLE,
  SQLITE_FUNCTION,
  SQLITE_INSERT,
  SQLITE_OK,
  SQLITE_PRAGMA,
  SQLITE_READ,
  SQLITE_REINDEX,
  SQLITE_RECURSIVE,
  SQLITE_SAVEPOINT,
  SQLITE_SELECT,
  SQLITE_TRANSACTION,
  SQLITE_UPDATE,
  SQLITE_DENY,
} = constants;

export const DB_QUERY_DEFAULT_LIMIT = 100;
export const DB_QUERY_MAX_LIMIT = 1000;
export const DB_QUERY_MAX_SQL_LENGTH = 64 * 1024;
export const DB_QUERY_VDBE_OP_LIMIT = 500_000;

export type DbErrorCode =
  | 'invalid_request'
  | 'table_not_found'
  | 'read_only_violation'
  | 'multiple_statements'
  | 'query_limit_exceeded'
  | 'query_failed'
  | 'schema_failed'
  | 'tables_failed';

export interface DbErrorBody {
  error: string;
  code: DbErrorCode;
}

export interface DbTableRow {
  name: string;
  sql: string | null;
}

export interface DbSchemaRow {
  table: string;
  columns: Array<{ name: string; type: string; notnull: number; dflt_value: string | null; pk: number }>;
  indexes: Array<{ name: string; unique: number; origin: string; partial: number }>;
  foreignKeys: Array<{ id: number; seq: number; table: string; from: string; to: string; on_update: string; on_delete: string; match: string }>;
}

export type DbJsonValue =
  | null
  | number
  | string
  | { type: 'bigint'; value: string }
  | { type: 'blob'; value: string; bytes: number };

export interface DbQueryRow {
  values: DbJsonValue[];
}

export interface DbQueryResponse {
  columns: string[];
  rows: DbQueryRow[];
  rowCount: number;
  truncated: boolean;
  limit: number;
}

export interface DatabaseBrowserDeps {
  db: Db;
  readonlyDb?: Db;
}

function errorBody(code: DbErrorCode, error: string): DbErrorBody {
  return { error, code };
}

function logDatabaseBrowserError(operation: string, error: unknown): void {
  const detail = error instanceof Error ? error.message : String(error);
  console.error(`[db-browser] ${operation} failed: ${detail}`);
}

function quoteIdentifier(identifier: string): string {
  return `"${identifier.replaceAll('"', '""')}"`;
}

function knownTables(db: Db): Set<string> {
  const rows = db.prepare(`
    SELECT name
    FROM sqlite_master
    WHERE type = 'table' AND name NOT LIKE 'sqlite_%'
    ORDER BY name
  `).all() as Array<{ name: string }>;
  return new Set(rows.map((row) => row.name));
}

export function listDatabaseTables({ db }: DatabaseBrowserDeps): { tables: DbTableRow[] } | DbErrorBody {
  try {
    const tables = db.prepare(`
      SELECT name, sql
      FROM sqlite_master
      WHERE type = 'table' AND name NOT LIKE 'sqlite_%'
      ORDER BY name
    `).all() as unknown as DbTableRow[];
    return { tables };
  } catch (error) {
    logDatabaseBrowserError('list tables', error);
    return errorBody('tables_failed', 'The database tables could not be loaded.');
  }
}

export function loadDatabaseSchema({ db }: DatabaseBrowserDeps, table: string | null): DbSchemaRow | DbErrorBody {
  if (!table) return errorBody('invalid_request', 'A table name is required.');

  try {
    if (!knownTables(db).has(table)) {
      return errorBody('table_not_found', 'The requested table was not found.');
    }

    const identifier = quoteIdentifier(table);
    const columns = db.prepare(`PRAGMA table_info(${identifier})`).all() as DbSchemaRow['columns'];
    const indexes = db.prepare(`PRAGMA index_list(${identifier})`).all() as DbSchemaRow['indexes'];
    const foreignKeys = db.prepare(`PRAGMA foreign_key_list(${identifier})`).all() as DbSchemaRow['foreignKeys'];
    return { table, columns, indexes, foreignKeys };
  } catch (error) {
    logDatabaseBrowserError('load schema', error);
    return errorBody('schema_failed', 'The table schema could not be loaded.');
  }
}

function firstKeyword(sql: string): string | null {
  let index = 0;
  while (index < sql.length) {
    while (/\s/.test(sql[index] ?? '')) index += 1;
    if (sql.startsWith('--', index)) {
      const newline = sql.indexOf('\n', index + 2);
      index = newline === -1 ? sql.length : newline + 1;
      continue;
    }
    if (sql.startsWith('/*', index)) {
      const end = sql.indexOf('*/', index + 2);
      if (end === -1) return null;
      index = end + 2;
      continue;
    }
    break;
  }
  const match = /^[A-Za-z]+/.exec(sql.slice(index));
  return match?.[0].toUpperCase() ?? null;
}

/**
 * Detects a second executable statement without treating semicolons inside
 * string literals or comments as statement separators. This is only a parser
 * boundary check; SQLite authorizer decisions remain the security boundary.
 */
function hasMultipleStatements(sql: string): boolean {
  let separatorSeen = false;
  let quote: "'" | '"' | '`' | ']' | null = null;
  for (let index = 0; index < sql.length; index += 1) {
    const char = sql[index];
    const next = sql[index + 1];

    if (quote) {
      if (quote === ']' && char === ']') {
        quote = null;
      } else if (char === quote) {
        if (next === quote) index += 1;
        else quote = null;
      }
      continue;
    }

    if (char === '-' && next === '-') {
      const newline = sql.indexOf('\n', index + 2);
      index = newline === -1 ? sql.length : newline;
      continue;
    }
    if (char === '/' && next === '*') {
      const end = sql.indexOf('*/', index + 2);
      if (end === -1) return true;
      index = end + 1;
      continue;
    }
    if (char === "'") quote = "'";
    else if (char === '"') quote = '"';
    else if (char === '`') quote = '`';
    else if (char === '[') quote = ']';
    else if (char === ';') separatorSeen = true;
    else if (separatorSeen && !/\s/.test(char)) return true;
  }
  return false;
}

function toJsonValue(value: unknown): DbJsonValue {
  if (value === null || typeof value === 'string' || typeof value === 'number') return value;
  if (typeof value === 'bigint') return { type: 'bigint', value: value.toString() };
  if (value instanceof Uint8Array) {
    return { type: 'blob', value: Buffer.from(value).toString('base64'), bytes: value.byteLength };
  }
  return String(value);
}

function authorizer(allowedTables: Set<string>) {
  const deniedActions = new Set([
    SQLITE_ALTER_TABLE,
    SQLITE_ANALYZE,
    SQLITE_ATTACH,
    SQLITE_CREATE_INDEX,
    SQLITE_CREATE_TABLE,
    SQLITE_CREATE_TEMP_INDEX,
    SQLITE_CREATE_TEMP_TABLE,
    SQLITE_CREATE_TEMP_TRIGGER,
    SQLITE_CREATE_TEMP_VIEW,
    SQLITE_CREATE_TRIGGER,
    SQLITE_CREATE_VIEW,
    SQLITE_CREATE_VTABLE,
    SQLITE_DELETE,
    SQLITE_DETACH,
    SQLITE_DROP_INDEX,
    SQLITE_DROP_TABLE,
    SQLITE_DROP_TEMP_INDEX,
    SQLITE_DROP_TEMP_TABLE,
    SQLITE_DROP_TEMP_TRIGGER,
    SQLITE_DROP_TEMP_VIEW,
    SQLITE_DROP_TRIGGER,
    SQLITE_DROP_VIEW,
    SQLITE_DROP_VTABLE,
    SQLITE_INSERT,
    SQLITE_PRAGMA,
    SQLITE_REINDEX,
    SQLITE_SAVEPOINT,
    SQLITE_TRANSACTION,
    SQLITE_UPDATE,
  ]);

  return (actionCode: number, arg1: string | null, arg2: string | null, dbName: string | null): number => {
    if (actionCode === SQLITE_SELECT || actionCode === SQLITE_RECURSIVE) return SQLITE_OK;
    if (actionCode === SQLITE_READ) {
      return dbName === 'main' && (arg1 === null || allowedTables.has(arg1)) ? SQLITE_OK : SQLITE_DENY;
    }
    if (actionCode === SQLITE_FUNCTION) {
      return arg2?.toLowerCase() === 'load_extension' ? SQLITE_DENY : SQLITE_OK;
    }
    return deniedActions.has(actionCode) ? SQLITE_DENY : SQLITE_DENY;
  };
}

function invalidQuery(sql: string): DbErrorBody | null {
  if (sql.length > DB_QUERY_MAX_SQL_LENGTH) {
    return errorBody('invalid_request', `SQL must be ${DB_QUERY_MAX_SQL_LENGTH} characters or fewer.`);
  }
  const keyword = firstKeyword(sql);
  if (keyword !== 'SELECT' && keyword !== 'WITH') {
    return errorBody('read_only_violation', 'Only one read-only SELECT or CTE query is allowed.');
  }
  if (hasMultipleStatements(sql)) {
    return errorBody('multiple_statements', 'Only one SQL statement is allowed per request.');
  }
  return null;
}

export function runDatabaseQuery({ db, readonlyDb }: DatabaseBrowserDeps, input: { sql?: unknown; limit?: unknown }): DbQueryResponse | DbErrorBody {
  if (typeof input.sql !== 'string' || !input.sql.trim()) {
    return errorBody('invalid_request', 'SQL is required and must be a non-empty string.');
  }
  if (input.limit !== undefined && (typeof input.limit !== 'number' || !Number.isSafeInteger(input.limit) || input.limit < 1 || input.limit > DB_QUERY_MAX_LIMIT)) {
    return errorBody('query_limit_exceeded', `Limit must be an integer from 1 to ${DB_QUERY_MAX_LIMIT}.`);
  }

  const sql = input.sql.trim();
  const invalid = invalidQuery(sql);
  if (invalid) return invalid;
  const limit: number = input.limit === undefined ? DB_QUERY_DEFAULT_LIMIT : input.limit;
  const queryDb = readonlyDb ?? db;
  let authorizerInstalled = false;

  try {
    const allowedTables = knownTables(queryDb);
    queryDb.setAuthorizer(authorizer(allowedTables));
    authorizerInstalled = true;

    const statement = queryDb.prepare(sql);
    statement.setReturnArrays(true);
    statement.setReadBigInts(true);
    const columns = statement.columns().map((column) => column.name);
    const rawRows: unknown[][] = [];
    const iterator = statement.iterate() as Iterator<unknown[]>;
    try {
      while (rawRows.length < limit + 1) {
        const next = iterator.next();
        if (next.done) break;
        rawRows.push(next.value);
      }
    } finally {
      iterator.return?.();
    }

    const truncated = rawRows.length > limit;
    const rows = rawRows.slice(0, limit).map((values) => ({ values: values.map(toJsonValue) }));
    return { columns, rows, rowCount: rows.length, truncated, limit };
  } catch (error) {
    logDatabaseBrowserError('query', error);
    return errorBody('query_failed', 'The read-only query could not be executed.');
  } finally {
    if (authorizerInstalled) {
      try {
        queryDb.setAuthorizer(null);
      } catch (error) {
        logDatabaseBrowserError('clear query authorizer', error);
      }
    }
  }
}

export function isDbError(value: unknown): value is DbErrorBody {
  return Boolean(value && typeof value === 'object' && 'error' in value && 'code' in value);
}
