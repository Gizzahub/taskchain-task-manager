// Package outputformat carries the version of one thing and one thing only:
// the shape of the JSON documents this CLI writes to stdout. It says nothing
// about anything this program stores.
//
// This is deliberately NOT the same axis as the schemaVersion found on this
// repository's on-disk journals and ledgers. Those are independently at 1
// through 4, and until this package existed both axes were spelled with the
// same JSON key, so a consumer reading schemaVersion: 1 could not tell which
// contract it was reading, while the two had already diverged numerically.
//
// The distinction is not cosmetic. Bumping Version is a public contract
// change: every consumer parsing our stdout must be told. Bumping a journal
// schema is a storage migration: already-written files on disk must be read,
// upgraded, or rejected. Different blast radius, different review, so they do
// not share a name.
//
// To re-derive the boundary rather than trust this comment, run:
//
//	grep -rnE "(Schema|Output)Version +\*?int" cmd internal | grep -v _test.go
//
// That prints all 30 version declarations across both axes. The 12 tagged
// json:"outputVersion" are this package's axis; the other 18, still tagged
// json:"schemaVersion" or json:"schema-version", are storage. Grepping only
// for SchemaVersion after this split shows storage alone and hides the very
// boundary it was meant to prove.
package outputformat

// Version numbers the stdout JSON document format. It is not a journal or
// ledger schema version.
const Version = 1
