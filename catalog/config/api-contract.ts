/**
 * Stable identifiers for the standalone Catalog API boundary.
 *
 * Consumers should fail closed when a response declares an unsupported version.
 * The header is additive, so existing payload shapes remain unchanged while the
 * Catalog and Router can evolve independently.
 */
// v2 replaced the /v1/alerts lifecycle wire shape with /v1/notifications read
// history. The route bodies and standalone.md said v2 while this constant kept
// saying v1, so the response header contradicted the response body it shipped
// with. One definition again; the two literals in app.ts now read it.
export const CATALOG_API_CONTRACT_VERSION = 'catalog-api-v2';
export const CATALOG_API_CONTRACT_HEADER = 'x-venom-catalog-contract';
