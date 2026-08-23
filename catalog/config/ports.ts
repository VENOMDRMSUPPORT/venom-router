/**
 * Default local endpoints for the standalone Venom Catalog product.
 *
 * The Catalog UI and API are separate processes. Venom Router is a separate
 * product and must not reuse either endpoint or open the Catalog database.
 */
export const CATALOG_BIND_HOST = '127.0.0.1';
export const CATALOG_API_PORT: number = 8791;
export const CATALOG_UI_PORT: number = 5173;

/** The Venom Router control-plane port, documented for collision checks only. */
export const VENOM_ROUTER_CONTROL_PORT: number = 8081;

if (
  CATALOG_API_PORT === CATALOG_UI_PORT
  || CATALOG_API_PORT === VENOM_ROUTER_CONTROL_PORT
  || CATALOG_UI_PORT === VENOM_ROUTER_CONTROL_PORT
) {
  throw new Error('Standalone Catalog and Venom Router ports must be distinct');
}
