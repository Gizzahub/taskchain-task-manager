# ADR-0001: fixture decision

- Status: Accepted
- Date: 2026-09-29
- Owners: parity fixture

## Context

The decisions validator needs a fully valid document as the fixture baseline.

## Decision

Pin this trivial accepted decision, and the README index that catalogues it, as the valid shape.

## Rationale

Every validator drift against this shape then shows up as a parity diff instead of a silent pass.

## Consequences

Fixture consumers must keep Status, the four sections, and the index row in agreement.
