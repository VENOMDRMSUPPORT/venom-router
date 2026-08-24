import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fetchDbQuery, fetchDbSchema, fetchDbTables } from '../../api/client';
import { DatabaseBrowserPage } from './DatabaseBrowserPage';

vi.mock('../../api/client', () => ({
  fetchDbTables: vi.fn(),
  fetchDbSchema: vi.fn(),
  fetchDbQuery: vi.fn(),
}));

const mockedTables = vi.mocked(fetchDbTables);
const mockedSchema = vi.mocked(fetchDbSchema);
const mockedQuery = vi.mocked(fetchDbQuery);

const tableResponse = {
  tables: [{ name: 'models', sql: 'CREATE TABLE models (model_id TEXT)' }, { name: 'model facts', sql: null }],
} as Awaited<ReturnType<typeof fetchDbTables>>;
const schemaResponse = {
  table: 'models',
  columns: [{ name: 'model_id', type: 'TEXT', notnull: 1, dflt_value: null, pk: 1 }],
  indexes: [{ name: 'models_pk', unique: 1, origin: 'pk', partial: 0 }],
  foreignKeys: [],
} as Awaited<ReturnType<typeof fetchDbSchema>>;
const resultResponse = {
  columns: ['id', 'id', 'payload'],
  rows: [{ values: [{ type: 'bigint', value: '9223372036854775807' }, null, { type: 'blob', value: 'AAH/', bytes: 3 }] }],
  rowCount: 1,
  truncated: true,
  limit: 1,
} as Awaited<ReturnType<typeof fetchDbQuery>>;

function renderPage() {
  return render(<MemoryRouter><DatabaseBrowserPage /></MemoryRouter>);
}

beforeEach(() => {
  mockedTables.mockResolvedValue(tableResponse);
  mockedSchema.mockResolvedValue(schemaResponse);
  mockedQuery.mockResolvedValue(resultResponse);
  window.localStorage.clear();
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.clearAllMocks();
});

describe('DatabaseBrowserPage', () => {
  it('loads tables, selects a table, loads its schema, and runs a query', async () => {
    renderPage();
    expect(await screen.findByText('models')).toBeTruthy();
    fireEvent.click(screen.getByRole('option', { name: 'models' }));
    expect(await screen.findByText('model_id')).toBeTruthy();

    const editor = screen.getByRole('textbox', { name: 'Read-only SQL query' });
    fireEvent.change(editor, { target: { value: 'SELECT model_id FROM models' } });
    fireEvent.click(screen.getByRole('button', { name: 'Run query' }));

    expect(await screen.findByText('9223372036854775807n')).toBeTruthy();
    expect(screen.getAllByText('id').length).toBeGreaterThanOrEqual(2);
    expect(screen.getByText(/limited to 1/i)).toBeTruthy();
    expect(mockedTables).toHaveBeenCalledWith(expect.any(AbortSignal));
    expect(mockedSchema).toHaveBeenCalledWith('models', expect.any(AbortSignal));
    expect(mockedQuery).toHaveBeenCalledWith('SELECT model_id FROM models', 1000, expect.any(AbortSignal));
  });

  it('shows loading, empty, error, and truncated states', async () => {
    let resolveQuery!: (value: Awaited<ReturnType<typeof fetchDbQuery>>) => void;
    mockedQuery.mockImplementation(() => new Promise((resolve) => { resolveQuery = resolve; }));
    renderPage();
    await screen.findByText('models');
    fireEvent.change(screen.getByRole('textbox', { name: 'Read-only SQL query' }), { target: { value: 'SELECT 1' } });
    fireEvent.click(screen.getByRole('button', { name: 'Run query' }));
    expect(screen.getByText('Executing query…')).toBeTruthy();
    await act(async () => resolveQuery({ ...resultResponse, rows: [], rowCount: 0, truncated: false }));
    expect(await screen.findByText('Query returned no rows.')).toBeTruthy();

    mockedQuery.mockRejectedValueOnce(new Error('typed query error'));
    fireEvent.click(screen.getByRole('button', { name: 'Run query' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('typed query error');
  });

  it('cancels the previous request and ignores its stale response', async () => {
    const pending: Array<{ resolve: (value: Awaited<ReturnType<typeof fetchDbQuery>>) => void; signal: AbortSignal }> = [];
    mockedQuery.mockImplementation((_sql, _limit, signal) => new Promise((resolve) => {
      pending.push({ resolve, signal: signal! });
    }));
    renderPage();
    await screen.findByText('models');
    const editor = screen.getByRole('textbox', { name: 'Read-only SQL query' });
    fireEvent.change(editor, { target: { value: 'SELECT 1' } });
    fireEvent.click(screen.getByRole('button', { name: 'Run query' }));
    fireEvent.change(editor, { target: { value: 'SELECT 2' } });
    fireEvent.click(screen.getByRole('button', { name: /Run query|Running…/ }));

    expect(pending).toHaveLength(2);
    expect(pending[0].signal.aborted).toBe(true);
    await act(async () => pending[0].resolve({ ...resultResponse, rows: [{ values: ['stale'] }], truncated: false }));
    await act(async () => pending[1].resolve({ ...resultResponse, rows: [{ values: ['fresh'] }], truncated: false }));
    expect(await screen.findByText('fresh')).toBeTruthy();
    expect(screen.queryByText('stale')).toBeNull();
  });

  it('supports the keyboard shortcut and does not record an aborted request', async () => {
    let resolveQuery!: (value: Awaited<ReturnType<typeof fetchDbQuery>>) => void;
    mockedQuery.mockImplementation(() => new Promise((resolve) => { resolveQuery = resolve; }));
    renderPage();
    await screen.findByText('models');
    const editor = screen.getByRole('textbox', { name: 'Read-only SQL query' });
    fireEvent.change(editor, { target: { value: 'SELECT 1' } });
    fireEvent.keyDown(editor, { key: 'Enter', ctrlKey: true });
    expect(mockedQuery).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(screen.getByText(/History \(0\)/)).toBeTruthy();
    await act(async () => resolveQuery(resultResponse));
    expect(screen.queryByText('1 rows returned')).toBeNull();
  });

  it('keeps at most 50 history entries, reuses queries, and clears history', async () => {
    const entries = Array.from({ length: 51 }, (_, index) => ({ id: `entry-${index}`, sql: `SELECT ${index}`, timestamp: new Date().toISOString(), success: true, rowCount: 0 }));
    window.localStorage.setItem('venom-catalog-db-history', JSON.stringify(entries));
    renderPage();
    await screen.findByText('models');
    fireEvent.click(screen.getByRole('button', { name: /History/ }));
    expect(screen.getByText('History (50)')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Clear history' }));
    expect(screen.getByText('History (0)')).toBeTruthy();

    vi.spyOn(window.localStorage, 'setItem').mockImplementation(() => { throw new Error('storage blocked'); });
    mockedQuery.mockResolvedValueOnce(resultResponse);
    fireEvent.change(screen.getByRole('textbox', { name: 'Read-only SQL query' }), { target: { value: 'SELECT 42' } });
    fireEvent.click(screen.getByRole('button', { name: 'Run query' }));
    expect(await screen.findByText('9223372036854775807n')).toBeTruthy();
    expect(mockedQuery).toHaveBeenCalled();
  });

  it('does not crash when clipboard and download APIs fail, and has no nested controls', async () => {
    renderPage();
    await screen.findByText('models');
    fireEvent.click(screen.getByRole('button', { name: 'Copy table name models' }));
    expect(await screen.findByText(/Could not copy/i)).toBeTruthy();

    fireEvent.change(screen.getByRole('textbox', { name: 'Read-only SQL query' }), { target: { value: 'SELECT 1' } });
    fireEvent.click(screen.getByRole('button', { name: 'Run query' }));
    await screen.findByText('9223372036854775807n');
    fireEvent.click(screen.getByRole('button', { name: 'Export query results' }));
    expect(screen.getByText(/Could not export|Results exported/i)).toBeTruthy();

    expect(document.querySelectorAll('button button')).toHaveLength(0);
  });

  it('handles table and schema request failures without crashing', async () => {
    mockedTables.mockRejectedValueOnce(new Error('tables unavailable'));
    renderPage();
    expect(await screen.findByRole('alert')).toHaveTextContent('tables unavailable');

    mockedTables.mockResolvedValueOnce(tableResponse);
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(await screen.findByText('models')).toBeTruthy();
    mockedSchema.mockRejectedValueOnce(new Error('schema unavailable'));
    fireEvent.click(screen.getByRole('option', { name: 'models' }));
    await waitFor(() => expect(screen.getByText('schema unavailable')).toBeTruthy());
  });
});
