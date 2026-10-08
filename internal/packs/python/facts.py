"""Pistos python pack facts worker: function metrics (metrics-v1) and import edges.

Speaks the pistos worker protocol: newline-delimited JSON-RPC 2.0 on stdin/stdout.
Uses only the standard library `ast` module of the interpreter that runs it.
"""

import ast
import json
import os
import sys

PROTOCOL = 1
ALGORITHM = "metrics-v1"
SKIP_DIRS = {"node_modules", "vendor", ".git", ".venv", "venv", "__pycache__", "bin", "obj",
             "dist", "build", ".tox", ".mypy_cache", ".ruff_cache", "site-packages"}
NESTING = (ast.If, ast.For, ast.AsyncFor, ast.While, ast.Try, ast.With, ast.AsyncWith, ast.Match)
if hasattr(ast, "TryStar"):
    NESTING = NESTING + (ast.TryStar,)
FUNCS = (ast.FunctionDef, ast.AsyncFunctionDef)


class Metrics:
    def __init__(self):
        self.statements = 0
        self.nesting = 0
        self.cyclomatic = 1

    def visit_body(self, stmts, depth):
        for i, s in enumerate(stmts):
            if i == 0 and is_docstring(s):
                continue
            self.visit(s, depth)

    def visit(self, node, depth):
        if isinstance(node, ast.stmt):
            self.statements += 1
        if isinstance(node, FUNCS + (ast.ClassDef,)):
            # Nested named definitions are separate units; decorators/defaults still belong here.
            for d in node.decorator_list:
                self.visit(d, depth)
            return
        if isinstance(node, ast.If):
            self.visit_if(node, depth)
            return
        if isinstance(node, NESTING):
            self.nesting = max(self.nesting, depth + 1)
            if isinstance(node, (ast.For, ast.AsyncFor, ast.While)):
                self.cyclomatic += 1
            if isinstance(node, (ast.Try,) + ((ast.TryStar,) if hasattr(ast, "TryStar") else ())):
                self.cyclomatic += len(node.handlers)
            if isinstance(node, ast.Match):
                self.cyclomatic += sum(1 for c in node.cases if not is_wildcard(c.pattern))
            for field, value in ast.iter_fields(node):
                if isinstance(value, list) and value and isinstance(value[0], ast.stmt):
                    self.visit_body(value, depth + 1)
                elif isinstance(value, list):
                    for v in value:
                        if isinstance(v, ast.AST):
                            self.visit(v, depth + 1 if isinstance(v, (ast.ExceptHandler, ast.match_case)) else depth)
                elif isinstance(value, ast.AST):
                    self.visit(value, depth)
            return
        if isinstance(node, (ast.ExceptHandler, ast.match_case)):
            for field, value in ast.iter_fields(node):
                if isinstance(value, list) and value and isinstance(value[0], ast.stmt):
                    self.visit_body(value, depth)
            return
        if isinstance(node, ast.BoolOp):
            self.cyclomatic += len(node.values) - 1
        elif isinstance(node, ast.IfExp):
            self.cyclomatic += 1
        elif isinstance(node, ast.comprehension):
            self.cyclomatic += 1 + len(node.ifs)
        for child in ast.iter_child_nodes(node):
            self.visit(child, depth)

    def visit_if(self, node, depth):
        self.cyclomatic += 1
        self.nesting = max(self.nesting, depth + 1)
        self.visit(node.test, depth)
        self.visit_body(node.body, depth + 1)
        orelse = node.orelse
        if len(orelse) == 1 and isinstance(orelse[0], ast.If):
            # elif continues the chain at the same depth
            self.statements += 1
            self.visit_if(orelse[0], depth)
        else:
            self.visit_body(orelse, depth + 1)


def is_docstring(s):
    return isinstance(s, ast.Expr) and isinstance(s.value, ast.Constant) and isinstance(s.value.value, str)


def is_wildcard(p):
    return isinstance(p, ast.MatchAs) and p.pattern is None and p.name is None


def param_count(fn, in_class):
    a = fn.args
    names = [x.arg for x in a.posonlyargs + a.args]
    n = len(names) + len(a.kwonlyargs) + (1 if a.vararg else 0) + (1 if a.kwarg else 0)
    if in_class and names and names[0] in ("self", "cls"):
        n -= 1
    return n


def functions(tree, path):
    out = []

    def walk(nodes, prefix, in_class):
        for node in nodes:
            if isinstance(node, FUNCS):
                name = prefix + node.name
                m = Metrics()
                m.visit_body(node.body, 0)
                out.append({
                    "path": path, "symbol": name, "line": node.lineno,
                    "end_line": getattr(node, "end_lineno", node.lineno) or node.lineno,
                    "statements": m.statements, "params": param_count(node, in_class),
                    "nesting": m.nesting, "cyclomatic": m.cyclomatic,
                })
                walk(node.body, name + ".", False)
            elif isinstance(node, ast.ClassDef):
                walk(node.body, prefix + node.name + ".", True)
            else:
                for child in ast.iter_child_nodes(node):
                    if isinstance(child, (ast.stmt,)):
                        walk([child], prefix, in_class)
    walk(tree.body, "", False)
    return out


class Resolver:
    def __init__(self, root):
        self.roots = [root] + ([os.path.join(root, "src")] if os.path.isdir(os.path.join(root, "src")) else [])
        self.root = root

    def module_file(self, base, parts):
        p = os.path.join(base, *parts)
        for cand in (p + ".py", os.path.join(p, "__init__.py")):
            if os.path.isfile(cand):
                return cand
        return None

    def absolute(self, dotted):
        parts = dotted.split(".")
        for r in self.roots:
            f = self.module_file(r, parts)
            if f:
                return f, True
        local = any(os.path.exists(os.path.join(r, parts[0])) or os.path.isfile(os.path.join(r, parts[0] + ".py")) for r in self.roots)
        return None, local

    def relative(self, file, level, module):
        base = os.path.dirname(file)
        for _ in range(level - 1):
            base = os.path.dirname(base)
        parts = module.split(".") if module else []
        return self.module_file(base, parts) if parts else os.path.join(base, "__init__.py") if os.path.isfile(os.path.join(base, "__init__.py")) else None


def imports(tree, file, rel, repo_root, res):
    out = []

    def edge(target, spec, line, known):
        if target:
            out.append({"from": rel, "to": to_repo(target, res.root, repo_root), "spec": spec, "line": line, "provenance": "syntax-only"})
        elif known:
            out.append({"from": rel, "spec": spec, "line": line, "provenance": "unknown"})

    for node in ast.walk(tree):
        if isinstance(node, ast.Import):
            for a in node.names:
                target, local = res.absolute(a.name)
                edge(target, a.name, node.lineno, local)
        elif isinstance(node, ast.ImportFrom):
            if node.level:
                mod = node.module or ""
                for a in node.names:
                    target = res.relative(file, node.level, (mod + "." + a.name).strip(".")) or res.relative(file, node.level, mod)
                    edge(target, "." * node.level + mod, node.lineno, True)
            else:
                for a in node.names:
                    target, local = res.absolute(node.module + "." + a.name)
                    if not target:
                        target, local = res.absolute(node.module)
                    edge(target, node.module, node.lineno, local)
        elif isinstance(node, ast.Call):
            fname = node.func.attr if isinstance(node.func, ast.Attribute) else getattr(node.func, "id", "")
            if fname in ("import_module", "__import__") and node.args:
                arg = node.args[0]
                if isinstance(arg, ast.Constant) and isinstance(arg.value, str):
                    target, local = res.absolute(arg.value)
                    edge(target, arg.value, node.lineno, local)
                else:
                    edge(None, "dynamic import", node.lineno, True)
    seen, unique = set(), []
    for e in out:
        k = (e["from"], e.get("to"), e["spec"], e["line"])
        if k not in seen:
            seen.add(k)
            unique.append(e)
    return unique


def to_repo(path, root, repo_root):
    rel = os.path.relpath(path, root).replace(os.sep, "/")
    return rel if repo_root in (".", "") else repo_root + "/" + rel


def analyze(root, repo_root, kind):
    funcs, edges, limits = [], [], []
    res = Resolver(root)
    errors = 0
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames[:] = sorted(d for d in dirnames if d not in SKIP_DIRS and not d.startswith("."))
        for name in sorted(filenames):
            if not name.endswith(".py"):
                continue
            full = os.path.join(dirpath, name)
            rel = to_repo(full, root, repo_root)
            try:
                with open(full, "rb") as fh:
                    tree = ast.parse(fh.read(), filename=full)
            except (SyntaxError, ValueError):
                errors += 1
                continue
            if kind == "metrics":
                funcs.extend(functions(tree, rel))
            else:
                edges.extend(imports(tree, full, rel, repo_root, res))
    if errors:
        limits.append(f"{errors} file(s) with syntax errors were not analyzed")
    if kind == "imports":
        limits.append("imports are resolved by file layout (project root and src/), not by the interpreter's sys.path")
    return {"algorithm": ALGORITHM, "nodes": "file", "functions": funcs, "imports": edges}, limits, errors


def main():
    pack, version = None, None
    for line in sys.stdin:
        msg = json.loads(line)
        method, mid, params = msg.get("method"), msg.get("id"), msg.get("params") or {}
        if method == "initialize":
            pack, version = params.get("pack"), params.get("pack_version")
            result = {"protocol": PROTOCOL, "pack": pack, "pack_version": version, "capabilities": ["metrics", "imports"]}
        elif method == "facts":
            facts, limits, errors = analyze(params["root"], params.get("repo_root", "."), params["kind"])
            result = {"status": "complete" if errors == 0 else "incomplete", "tree": params["tree"],
                      "project": params["project"], "facts": facts, "limitations": limits}
        elif method == "shutdown":
            result = None
        else:
            if mid is not None:
                reply = {"jsonrpc": "2.0", "id": mid, "error": {"code": -32601, "message": "unknown method " + str(method)}}
                sys.stdout.write(json.dumps(reply) + "\n")
                sys.stdout.flush()
            continue
        sys.stdout.write(json.dumps({"jsonrpc": "2.0", "id": mid, "result": result}) + "\n")
        sys.stdout.flush()
        if method == "shutdown":
            return


if __name__ == "__main__":
    main()
