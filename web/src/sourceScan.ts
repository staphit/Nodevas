/**
 * Budget for a test that walks the whole of web/src. Alone such a scan is
 * well under a second; under a full parallel run beside the Go suite it has
 * passed vitest's 5 s default and failed a correct tree.
 */
export const SOURCE_SCAN_TIMEOUT = 30_000;
