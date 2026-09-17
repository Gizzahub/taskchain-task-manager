# Clone owner rejoin

Owner rejoin is a clone-local authority migration, not a shared-board policy
activation. A source clone's namespace and policy authority remain provenance;
an independent target clone receives a new namespace and, where applicable, a
new authority ID.

d1 is preparation only. It defines strict, content-addressed plan, receipt,
and exact original/target-byte payload codecs, reservation floors, and the
permanent storage protocol 6 barrier. It does not publish artifacts, rewrite
an owner, or expose a runtime command. Ordinary runtime operations refuse
protocol 6 until a completed owner-rejoin receipt is implemented.

d2 will connect filesystem publication and recovery sequencing. d3 will add
the CLI/apply/resume surface. Until then a payload or a pending receipt is not
authority and must not be used to alter a board.
