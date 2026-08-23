/**
 * Stable identifiers for the standalone Catalog API boundary.
 *
 * Consumers should fail closed when a response declares an unsupported version.
 * The header is additive, so existing payload shapes remain unchanged while the
 * Catalog and Router can evolve independently.
 */
export const CATALOG_API_CONTRACT_VERSION = 'catalog-api-v1';
export const CATALOG_API_CONTRACT_HEADER = 'x-venom-catalog-contract';
