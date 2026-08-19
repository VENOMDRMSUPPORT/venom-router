/**
 * One writer, enforced rather than remembered.
 *
 * The service owns `data/catalog.db`. A terminal batch that opens its own
 * connection while the service runs is a second writer, and a guarantee that
 * depends on someone remembering to stop the service first is not a guarantee.
 * On 2026-08-19 a two-hour evaluation batch ran exactly that way; the database
 * survived on SQLite's locking alone, which is luck rather than design.
 *
 * The check is a loopback probe of the service's own port. That is a heuristic,
 * not a proof: a service started on a non-default port is not detected. It
 * catches the case that actually happens — the service left running from the
 * tray or a previous shell — and it never produces a false positive, because
 * nothing else is allowed to bind the catalog's loopback port.
 */
import { connect } from 'node:net';
import { BIND_HOST, DEFAULT_PORT } from '../server/index.ts';

const PROBE_TIMEOUT_MS = 500;

export function portIsListening(port: number): Promise<boolean> {
  return new Promise((resolve) => {
    const socket = connect({ host: BIND_HOST, port });
    let settled = false;
    const settle = (listening: boolean) => {
      if (settled) return;
      settled = true;
      socket.destroy();
      resolve(listening);
    };
    socket.setTimeout(PROBE_TIMEOUT_MS);
    socket.once('connect', () => settle(true));
    socket.once('timeout', () => settle(false));
    socket.once('error', () => settle(false));
  });
}

/**
 * Throws when the catalog service is listening, so a batch refuses to start
 * rather than becoming a second writer. The message names the alternative:
 * the same work can be driven from the dashboard, where the service runs it.
 */
export async function assertServiceNotListening(
  probe: (port: number) => Promise<boolean> = portIsListening,
): Promise<void> {
  if (await probe(DEFAULT_PORT)) {
    throw new Error(
      `service_is_listening: the catalog service holds ${BIND_HOST}:${DEFAULT_PORT} and owns the database. `
      + 'Stop it first, or run the evaluation from the dashboard.',
    );
  }
}
