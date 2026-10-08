// Pistos jsts pack facts worker: function metrics (metrics-v1) and import edges for JS/TS.
// Speaks the pistos worker protocol (newline-delimited JSON-RPC 2.0) and parses with the pinned
// oxc-parser installed next to this script by `pistos plugins install`.
import { createRequire } from "node:module";
import { readFileSync, readdirSync, statSync, existsSync } from "node:fs";
import { join, relative, dirname, sep } from "node:path";
import { createInterface } from "node:readline";

const require = createRequire(import.meta.url);
const { parseSync } = require("oxc-parser");

const PROTOCOL = 1;
const ALGORITHM = "metrics-v1";
const SKIP = new Set(["node_modules", "vendor", ".git", "bin", "obj", "dist", "build", "coverage", ".next", "out"]);
const SOURCE = /\.(ts|tsx|mts|cts|js|jsx|mjs|cjs)$/;
const RESOLVE_EXT = [".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs"];
const LOOPS = new Set(["ForStatement", "ForInStatement", "ForOfStatement", "WhileStatement", "DoWhileStatement"]);
const NESTING = new Set([...LOOPS, "SwitchStatement", "TryStatement"]);
const FUNCTION_TYPES = new Set(["FunctionDeclaration", "FunctionExpression", "ArrowFunctionExpression"]);

function children(node) {
  const out = [];
  for (const [k, v] of Object.entries(node)) {
    if (k === "type" || k === "start" || k === "end" || v === null || typeof v !== "object") continue;
    if (Array.isArray(v)) {
      for (const c of v) if (c && typeof c.type === "string") out.push(c);
    } else if (typeof v.type === "string") out.push(v);
  }
  return out;
}

function isStatement(n) {
  if (n.type === "BlockStatement" || n.type === "EmptyStatement") return false;
  return n.type.endsWith("Statement") || n.type === "VariableDeclaration" || n.type === "FunctionDeclaration" || n.type === "ClassDeclaration";
}

class Metrics {
  constructor(units) {
    this.units = units;
    this.statements = 0;
    this.nesting = 0;
    this.cyclomatic = 1;
  }
  visit(n, depth) {
    if (isStatement(n)) this.statements++;
    if (this.units.has(n)) return; // nested named functions are measured on their own
    if (n.type === "IfStatement") return this.visitIf(n, depth);
    if (NESTING.has(n.type)) {
      this.nesting = Math.max(this.nesting, depth + 1);
      if (LOOPS.has(n.type)) this.cyclomatic++;
      for (const c of children(n)) {
        const body = c === n.body || c === n.block || c === n.finalizer || c === n.handler || n.cases?.includes(c);
        // Loop headers (initializers, iteration variables) are not statements of their own.
        if (c === n.init || c === n.left) this.visitHeader(c, depth);
        else this.visit(c, body ? depth + 1 : depth);
      }
      return;
    }
    if (n.type === "SwitchCase" && n.test) this.cyclomatic++;
    if (n.type === "CatchClause") this.cyclomatic++;
    if (n.type === "ConditionalExpression") this.cyclomatic++;
    if (n.type === "LogicalExpression") this.cyclomatic++;
    for (const c of children(n)) this.visit(c, depth);
  }
  visitHeader(n, depth) {
    const before = this.statements;
    this.visit(n, depth);
    this.statements = before;
  }
  visitIf(n, depth) {
    this.cyclomatic++;
    this.nesting = Math.max(this.nesting, depth + 1);
    this.visit(n.test, depth);
    this.visit(n.consequent, depth + 1);
    if (n.alternate) {
      if (n.alternate.type === "IfStatement") {
        this.statements++; // else-if continues the chain at the same depth
        this.visitIf(n.alternate, depth);
      } else this.visit(n.alternate, depth + 1);
    }
  }
}

function lineIndex(src) {
  const starts = [0];
  for (let i = 0; i < src.length; i++) if (src.charCodeAt(i) === 10) starts.push(i + 1);
  return (offset) => {
    let lo = 0, hi = starts.length - 1;
    while (lo < hi) {
      const mid = (lo + hi + 1) >> 1;
      if (starts[mid] <= offset) lo = mid; else hi = mid - 1;
    }
    return lo + 1;
  };
}

// collectUnits finds named functions: declarations, class methods/properties and functions assigned
// to identifiers. Anonymous callbacks stay part of their enclosing unit.
function collectUnits(program) {
  const units = new Map();
  const walk = (n, prefix) => {
    switch (n.type) {
      case "FunctionDeclaration":
        if (n.id && n.body) { units.set(n, prefix + n.id.name); walk(n.body, prefix + n.id.name + "."); return; }
        break;
      case "ClassDeclaration":
      case "ClassExpression":
        if (n.id) { for (const c of children(n)) walk(c, prefix + n.id.name + "."); return; }
        break;
      case "MethodDefinition":
      case "PropertyDefinition": {
        const key = n.key?.name ?? n.key?.value;
        const fn = n.value;
        if (key !== undefined && fn && FUNCTION_TYPES.has(fn.type) && fn.body) {
          const name = prefix + (n.kind === "get" || n.kind === "set" ? n.kind + " " : "") + key;
          units.set(fn, name);
          walk(fn.body, name + ".");
          return;
        }
        break;
      }
      case "VariableDeclarator":
        if (n.id?.type === "Identifier" && n.init && FUNCTION_TYPES.has(n.init.type) && n.init.body) {
          units.set(n.init, prefix + n.id.name);
          walk(n.init.body, prefix + n.id.name + ".");
          return;
        }
        break;
    }
    for (const c of children(n)) walk(c, prefix);
  };
  walk(program, "");
  return units;
}

function functions(program, src, path) {
  const line = lineIndex(src);
  const units = collectUnits(program);
  const out = [];
  for (const [fn, name] of units) {
    const m = new Metrics(units);
    const body = fn.body;
    if (body.type === "BlockStatement") for (const s of body.body) m.visit(s, 0);
    else m.visit(body, 0);
    out.push({
      path, symbol: name, line: line(fn.start), end_line: line(fn.end),
      statements: m.statements, params: fn.params.filter((p) => p.type !== "TSThisParameter").length,
      nesting: m.nesting, cyclomatic: m.cyclomatic,
    });
  }
  out.sort((a, b) => a.line - b.line || a.symbol.localeCompare(b.symbol));
  return out;
}

function resolveRelative(fromFile, spec) {
  const base = join(dirname(fromFile), spec);
  const candidates = [base, ...RESOLVE_EXT.map((e) => base + e), ...RESOLVE_EXT.map((e) => join(base, "index" + e))];
  const js = spec.match(/\.(m|c)?jsx?$/);
  if (js) {
    const stem = base.slice(0, -js[0].length);
    candidates.push(...[".ts", ".tsx", ".mts", ".cts"].map((e) => stem + e));
  }
  for (const c of candidates) {
    try { if (statSync(c).isFile()) return c; } catch {}
  }
  return null;
}

function imports(program, src, file, rel, root, repoRoot) {
  const line = lineIndex(src);
  const out = [];
  const add = (spec, offset, dynamic) => {
    if (spec === null) { out.push({ from: rel, spec: "dynamic import", line: line(offset), provenance: "unknown" }); return; }
    if (spec.startsWith(".")) {
      const target = resolveRelative(file, spec);
      out.push(target
        ? { from: rel, to: toRepo(target, root, repoRoot), spec, line: line(offset), provenance: "syntax-only" }
        : { from: rel, spec, line: line(offset), provenance: "unknown" });
    } else if (/^(@\/|~\/|#)/.test(spec)) {
      out.push({ from: rel, spec, line: line(offset), provenance: "unknown" });
    }
  };
  const literal = (n) => (n && (n.type === "Literal" || n.type === "StringLiteral") && typeof n.value === "string" ? n.value : null);
  const walk = (n) => {
    switch (n.type) {
      case "ImportDeclaration":
      case "ExportAllDeclaration":
        add(literal(n.source), n.start);
        return;
      case "ExportNamedDeclaration":
        if (n.source) add(literal(n.source), n.start);
        break;
      case "ImportExpression":
        add(literal(n.source), n.start);
        break;
      case "TSImportEqualsDeclaration":
        if (n.moduleReference?.expression) add(literal(n.moduleReference.expression), n.start);
        break;
      case "CallExpression":
        if (n.callee?.type === "Identifier" && n.callee.name === "require" && n.arguments.length > 0) add(literal(n.arguments[0]), n.start);
        break;
    }
    for (const c of children(n)) walk(c);
  };
  walk(program);
  return out;
}

function toRepo(p, root, repoRoot) {
  const rel = relative(root, p).split(sep).join("/");
  return repoRoot === "." || repoRoot === "" ? rel : repoRoot + "/" + rel;
}

function sourceFiles(dir, acc = []) {
  for (const name of readdirSync(dir).sort()) {
    if (SKIP.has(name) || name.startsWith(".")) continue;
    const full = join(dir, name);
    const st = statSync(full);
    if (st.isDirectory()) sourceFiles(full, acc);
    else if (SOURCE.test(name) && !name.endsWith(".d.ts")) acc.push(full);
  }
  return acc;
}

function analyze(root, repoRoot, kind) {
  const facts = { algorithm: ALGORITHM, nodes: "file", functions: [], imports: [] };
  const limitations = [];
  let errors = 0;
  for (const file of sourceFiles(root)) {
    const src = readFileSync(file, "utf8");
    const res = parseSync(file, src);
    if (res.errors.length > 0) { errors++; continue; }
    const rel = toRepo(file, root, repoRoot);
    if (kind === "metrics") facts.functions.push(...functions(res.program, src, rel));
    else facts.imports.push(...imports(res.program, src, file, rel, root, repoRoot));
  }
  if (errors) limitations.push(`${errors} file(s) with syntax errors were not analyzed`);
  if (kind === "imports") limitations.push("relative specifiers are resolved by file layout; tsconfig path aliases and package exports are not resolved");
  return { facts, limitations, errors };
}

const rl = createInterface({ input: process.stdin });
rl.on("line", (line) => {
  const msg = JSON.parse(line);
  const reply = (result) => process.stdout.write(JSON.stringify({ jsonrpc: "2.0", id: msg.id, result }) + "\n");
  switch (msg.method) {
    case "initialize":
      reply({ protocol: PROTOCOL, pack: msg.params.pack, pack_version: msg.params.pack_version, capabilities: ["metrics", "imports"] });
      break;
    case "facts": {
      const p = msg.params;
      const { facts, limitations, errors } = analyze(p.root, p.repo_root ?? ".", p.kind);
      reply({ status: errors ? "incomplete" : "complete", tree: p.tree, project: p.project, facts, limitations });
      break;
    }
    case "shutdown":
      reply(null);
      rl.close();
      break;
    default:
      if (msg.id !== undefined) process.stdout.write(JSON.stringify({ jsonrpc: "2.0", id: msg.id, error: { code: -32601, message: "unknown method" } }) + "\n");
  }
});
