package entitycatalog

// canonicalTestContentSHA is a valid 64-hex content address. Catalog
// materialization rejects non-SHA values at the boundary, so fixtures must use
// a real shape instead of a placeholder such as "sha-mj".
const canonicalTestContentSHA = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
