# Input immutability resolution

Resolved on 2026-07-15.

The previous line-oriented regular-expression scanner reported 129 matches in 42 files because it treated every variable named `req`, `input`, `request`, or `params` as a caller-owned input parameter.

The canonical scanner now uses Go AST object identity and reports only caller-visible mutation of pointer input contracts whose type name ends in `Input`, `Request`, `Params`, or `Command`.

Excluded by design:

- request DTOs decoded into local variables by HTTP handlers;
- value parameters normalized inside a function;
- local output DTOs such as `req := PublishRequest{}`;
- shadowed local variables;
- `net/http.Request` transport state;
- test files.

Detected and prevented:

- whole pointer-input reassignment such as `*input = ...`;
- direct or nested field assignment through a tracked pointer input;
- indexed assignment through a tracked pointer input;
- increment/decrement mutation;
- mutation from a nested closure capturing the pointer input.

The scanner has synthetic regression coverage and a canonical-tree regression test that requires zero caller-visible input mutations in `internal/application/**` and `internal/api/**`.
