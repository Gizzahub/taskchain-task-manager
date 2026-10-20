// Package roleprotocol is the pure codec and verification layer of file-role
// protocol v1 (specs/task-file-roles §14.1–§14.5): strict decoding, the E
// encoding and domain digests, selector/binding/history verification, support
// evidence and operator acknowledgement shapes, the source tuple and legacy
// transport, and the closed persistent role owner.
//
// The package is deliberately inactive. No command, Store writer, gate,
// reader or runtime path may import it until the publication (§14.6) and
// reader/preview/lint/gate (§14.7) work is separately accepted; an import
// boundary test enforces that. It performs no I/O, reads no clock, and
// verifying a document here is neither eligibility nor activation: digests and
// shapes are not a cryptographic proof that a recorded acceptance happened.
package roleprotocol
