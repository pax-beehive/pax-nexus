# Operations

The Operations context gives installation operators a bounded view of service
activity, diagnostics, and storage growth without turning product content into
administrative logs.

## Language

**Operation Event**:
A bounded record of one authenticated service or worker attempt, its outcome,
duration, safe counts, and an optional reference to product-owned diagnostics.
_Avoid_: Audit Event, request log, trace payload

**Operation Outcome**:
One of succeeded, rejected, failed, timed_out, or cancelled. A valid empty
recall and an idempotent write replay are succeeded outcomes.
_Avoid_: HTTP status, recall hit

**Storage Snapshot**:
A timestamped, versioned measurement of logical inventory and PostgreSQL
physical allocation, with explicit freshness and completeness.
_Avoid_: Database backup, live counter

**Logical Inventory**:
The current domain objects and content bytes attributed to one storage
component, independent of indexes and reusable PostgreSQL allocation.
_Avoid_: Disk size, table size

**Physical Allocation**:
The PostgreSQL bytes allocated to a storage component's tables, indexes, and
TOAST data at snapshot time.
_Avoid_: Live content size, reclaimable space

**Operations Activity**:
The recent, filterable projection of Operation Events shown to an operator.
_Avoid_: Audit Trail, raw logs

## Relationships

- An **Operation Event** may reference a product-owned Recall Observation or
  Extraction Run but never copies its content.
- A **Storage Snapshot** contains one **Logical Inventory** and one **Physical
  Allocation** measurement for each recognized storage component.
- **Operations Activity** is retained for a bounded period; an identity
  **Audit Event** remains a separate governance record.

## Flagged ambiguities

- "write count" is not canonical. Use Observation requests, newly written
  Session Events, and admitted Team Note revisions as separate measurements.
- "current size" must say whether it means Logical Inventory or Physical
  Allocation and must include snapshot freshness.
