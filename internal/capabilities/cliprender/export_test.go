package cliprender

// ChrononSidecarFixture exposes the canonical sidecar fixture to the external
// integration test package. The persistence test lives in cliprender_test so
// it can import the SQLite performance store without creating an
// overlays → cliprender dependency cycle through package-internal tests.
var ChrononSidecarFixture = chrononSidecarFixture
