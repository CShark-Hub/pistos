# Function metrics (`metrics-v1`)

The `metrics` check measures every **named** function. Anonymous functions, lambdas and closures
belong to the enclosing named function; nested named functions are separate units and do not count
towards their parent (except for the one statement that declares them).

| Metric | Definition |
|---|---|
| `statements` | Statement nodes in the body, recursively. Blocks, empty statements, labels and loop/if/switch **headers** (initializers, post statements, iteration variables) are not statements; an `else if` counts as one statement; a Python docstring does not count; an expression-bodied member counts as one. Formatting-invariant by construction. |
| `params` | Declared parameters, variadic/rest counting once. Go receivers, Python `self`/`cls` of methods and TypeScript `this` parameters are excluded. |
| `nesting` | Maximum depth of nested control blocks. An `else if`/`elif` continues its chain at the same depth. |
| `cyclomatic` | 1 + decision points (below). Not equivalent to SonarSource cognitive complexity. |

Decision points and nesting blocks per language:

| | Decision points (+1 each) | Nesting blocks |
|---|---|---|
| Go | `if`, `else if`, `for`, `range`, non-default `case`, non-default `select` case, `&&`, `\|\|` | `if`, `for`/`range`, `switch`, type switch, `select` |
| Python | `if`, `elif`, `for`, `while`, `except` clause, non-wildcard `case`, each extra `and`/`or` operand, conditional expression, each comprehension `for` and `if` | `if`, `for`, `while`, `try`, `with`, `match` |
| JS/TS | `if`, `else if`, `for`/`for-in`/`for-of`/`while`/`do`, non-default `case`, `catch`, `?:`, `&&`, `\|\|`, `??` | `if`, loops, `switch`, `try` |
| C# | `if`, `else if`, `for`/`foreach`/`while`/`do`, `case` labels, non-discard switch-expression arms, `catch`, `?:`, `&&`, `\|\|`, `??` | `if`, loops, `switch`, `try` |

Units: Go functions and methods (`Type.Method`); Python `def`/`async def` qualified by class and
enclosing function (`Shop.total`, `outer.inner`); JS/TS function declarations, class methods and
accessors, and functions assigned to identifiers or class properties; C# methods, constructors
(`ctor`), local functions, operators and accessors with bodies.

Backends: Go uses `go/ast` inside pistos; Python uses the standard `ast` module of the locked `python3`;
JS/TS uses the pinned `oxc-parser` 0.153.0; C# uses a small worker compiled against the Roslyn bundled
with the installed .NET SDK. Fixtures with hand-computed values for every language are in
`testdata/fixtures/metrics-langs` and `internal/facts/testdata`.

## Thresholds, deltas and gating

```toml
[metrics]
gate = false        # true turns new/worsened debt into warnings that can block
cyclomatic = 10
nesting = 4
params = 5
statements = 50
```

A function above a threshold yields a `metrics/<name>` finding anchored at its declaration. Against the
baseline it is `new` (newly above the threshold), `worsened` (above before and higher now; the report
shows the previous value) or `existing` (unchanged debt, not listed by default). Baseline findings in
renamed files are re-keyed to the new path, and a function moved to another file is matched by symbol
and declaration line, so renames, moves and reformatting do not appear as new debt.
