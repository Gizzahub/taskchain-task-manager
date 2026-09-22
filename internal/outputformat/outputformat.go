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
// The stdout axis has exactly one attachment point: Encode in this package
// splices outputVersion into the top level of every document a command writes.
// No output type declares the field, deliberately — several of them are also
// written verbatim into on-disk ledgers (ClaimRecord into the claims ledger,
// whose size is budgeted), so a field on the type would push a stdout contract
// into storage.
//
// To re-derive the boundary rather than trust this comment, run:
//
//	grep -rn "outputVersion" cmd internal --include='*.go' | grep -v _test.go
//
// Quote the --include pattern. Unquoted, zsh expands it against the current
// directory and aborts the command with "no matches found" when nothing there
// ends in .go, printing nothing — which reads exactly like a true zero result.
//
// That should print this package alone. Every other version declaration in the
// tree, tagged json:"schemaVersion" or json:"schema-version", is storage.
package outputformat

// Version numbers the stdout JSON document format. It is not a journal or
// ledger schema version.
const Version = 1
