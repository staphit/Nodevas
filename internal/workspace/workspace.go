// Package workspace holds the path constants that both persistence layers
// agree on.
//
// A workspace is written by two of them: the file store (graph.yaml, node
// bodies) and the SQLite database (accounts, audit, settings, search index).
// Both need to know where a workspace keeps the data that is not the user's
// content, and neither should have to import the other to find out — the
// database is the lower layer, so having it reach up into the store for a
// string was a dependency pointing the wrong way. The constants live here, in
// a package that imports nothing.
package workspace

// DataDir is the per-workspace directory for everything this program owns but
// the user does not edit by hand.
const DataDir = ".vised"
