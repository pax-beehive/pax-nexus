# Engineering rules

These rules apply to all handwritten Go code in this repository.

## Interfaces and generation

- Hertz is the HTTP framework.
- Thrift files under `idl/` are the source of truth for HTTP interfaces.
- Run `make generate-init` only when bootstrapping Hertz generation. After that,
  run `make generate` for every IDL change.
- Generated model and router code must not contain handwritten domain logic.
- Run `make mocks` after changing a mocked interface.

## Tests

- New and changed handwritten packages must keep aggregate unit-test coverage
  at or above 75 percent. `make coverage` enforces the threshold.
- Use `github.com/stretchr/testify/suite` for package/module test fixtures.
- Use table-driven subtests for input matrices, validation cases, lifecycle
  transitions, and error cases.
- Test observable behavior through module interfaces. Avoid tests coupled to
  private implementation details.
- PostgreSQL adapter tests run against PostgreSQL, not SQLite substitutes.

## Errors and complexity

- Code comments must use English only and must not contain emoji.
- Handle every returned error. Do not discard errors with `_` unless an
  unavoidable best-effort cleanup is accompanied by a comment and test.
- Wrap errors with operation context using `%w`; callers use `errors.Is` or
  `errors.As` rather than matching strings.
- Cyclomatic complexity must not exceed 20.
- Cognitive complexity must not exceed 25.
- Split orchestration from decisions before adding linter suppressions.
- `//nolint` requires the exact linter name and a concrete justification.
- Run `make lint test` before handing off code.
