// Pistos csharp pack facts worker: function metrics (metrics-v1) and namespace import edges.
// Speaks the pistos worker protocol: newline-delimited JSON-RPC 2.0 on stdin/stdout.
using System.Text.Json;
using System.Text.Json.Nodes;
using Microsoft.CodeAnalysis;
using Microsoft.CodeAnalysis.CSharp;
using Microsoft.CodeAnalysis.CSharp.Syntax;

static class Worker
{
    const string Algorithm = "metrics-v1";
    static readonly HashSet<string> Skip = new() { "bin", "obj", "node_modules", ".git", "artifacts" };

    static void Main()
    {
        string? line;
        while ((line = Console.In.ReadLine()) != null)
        {
            var msg = JsonNode.Parse(line)!.AsObject();
            var method = (string?)msg["method"];
            var id = msg["id"]?.DeepClone();
            var p = msg["params"] as JsonObject;
            JsonNode? result;
            switch (method)
            {
                case "initialize":
                    result = new JsonObject { ["protocol"] = 1, ["pack"] = (string?)p?["pack"], ["pack_version"] = (string?)p?["pack_version"], ["capabilities"] = new JsonArray("metrics", "imports") };
                    break;
                case "facts":
                    result = Analyze((string)p!["root"]!, (string?)p["repo_root"] ?? ".", (string)p["kind"]!, (string)p["tree"]!, (string)p["project"]!);
                    break;
                case "shutdown":
                    result = null;
                    break;
                default:
                    if (id != null) Write(new JsonObject { ["jsonrpc"] = "2.0", ["id"] = id, ["error"] = new JsonObject { ["code"] = -32601, ["message"] = "unknown method" } });
                    continue;
            }
            Write(new JsonObject { ["jsonrpc"] = "2.0", ["id"] = id, ["result"] = result });
            if (method == "shutdown") return;
        }
    }

    static void Write(JsonObject o)
    {
        Console.Out.Write(o.ToJsonString() + "\n");
        Console.Out.Flush();
    }

    static IEnumerable<string> Files(string dir)
    {
        foreach (var f in Directory.GetFiles(dir, "*.cs").OrderBy(x => x, StringComparer.Ordinal)) yield return f;
        foreach (var d in Directory.GetDirectories(dir).OrderBy(x => x, StringComparer.Ordinal))
        {
            var name = Path.GetFileName(d);
            if (Skip.Contains(name) || name.StartsWith('.')) continue;
            foreach (var f in Files(d)) yield return f;
        }
    }

    static string ToRepo(string full, string root, string repoRoot)
    {
        var rel = Path.GetRelativePath(root, full).Replace(Path.DirectorySeparatorChar, '/');
        return repoRoot is "." or "" ? rel : repoRoot + "/" + rel;
    }

    static JsonObject Analyze(string root, string repoRoot, string kind, string tree, string project)
    {
        var functions = new JsonArray();
        var imports = new JsonArray();
        var limitations = new JsonArray();
        var parsed = new List<(string Rel, SyntaxTree Tree)>();
        int errors = 0;
        foreach (var file in Files(root))
        {
            var t = CSharpSyntaxTree.ParseText(File.ReadAllText(file), path: file);
            if (t.GetDiagnostics().Any(d => d.Severity == DiagnosticSeverity.Error)) errors++;
            parsed.Add((ToRepo(file, root, repoRoot), t));
        }
        if (kind == "metrics")
        {
            foreach (var (rel, t) in parsed)
                foreach (var f in Metrics.Units(t.GetRoot(), rel)) functions.Add(f);
        }
        else
        {
            var declared = new Dictionary<string, SortedSet<string>>();
            foreach (var (rel, t) in parsed)
                foreach (var ns in t.GetRoot().DescendantNodes().OfType<BaseNamespaceDeclarationSyntax>())
                {
                    var name = ns.Name.ToString();
                    if (!declared.TryGetValue(name, out var dirs)) declared[name] = dirs = new SortedSet<string>(StringComparer.Ordinal);
                    dirs.Add(DirOf(rel));
                }
            foreach (var (rel, t) in parsed)
                foreach (var u in t.GetRoot().DescendantNodes().OfType<UsingDirectiveSyntax>())
                {
                    if (!u.StaticKeyword.IsKind(SyntaxKind.None) || u.Name == null) continue;
                    var name = u.Name.ToString();
                    if (u.Alias != null && !declared.ContainsKey(name)) name = name.Contains('.') ? name[..name.LastIndexOf('.')] : name;
                    if (!declared.TryGetValue(name, out var dirs)) continue;
                    var line = u.GetLocation().GetLineSpan().StartLinePosition.Line + 1;
                    foreach (var d in dirs)
                        if (d != DirOf(rel))
                            imports.Add(new JsonObject { ["from"] = rel, ["to"] = d, ["spec"] = name, ["line"] = line, ["provenance"] = "syntax-only" });
                }
            limitations.Add("edges come from using directives mapped to directories declaring the namespace; fully qualified names and reflection are not traced");
        }
        if (errors > 0) limitations.Add($"{errors} file(s) have syntax errors; their metrics cover the recoverable syntax only");
        return new JsonObject
        {
            ["status"] = "complete", ["tree"] = tree, ["project"] = project, ["limitations"] = limitations,
            ["facts"] = new JsonObject { ["algorithm"] = Algorithm, ["nodes"] = "dir", ["functions"] = functions, ["imports"] = imports },
        };
    }

    static string DirOf(string rel) => rel.Contains('/') ? rel[..rel.LastIndexOf('/')] : ".";
}

static class Metrics
{
    public static IEnumerable<JsonObject> Units(SyntaxNode root, string path)
    {
        foreach (var n in root.DescendantNodes())
        {
            string? name = null;
            ParameterListSyntax? ps = null;
            SyntaxNode? body = null;
            switch (n)
            {
                case MethodDeclarationSyntax m: name = m.Identifier.Text; ps = m.ParameterList; body = (SyntaxNode?)m.Body ?? m.ExpressionBody; break;
                case ConstructorDeclarationSyntax c: name = "ctor"; ps = c.ParameterList; body = (SyntaxNode?)c.Body ?? c.ExpressionBody; break;
                case LocalFunctionStatementSyntax l: name = l.Identifier.Text; ps = l.ParameterList; body = (SyntaxNode?)l.Body ?? l.ExpressionBody; break;
                case OperatorDeclarationSyntax o: name = "operator " + o.OperatorToken.Text; ps = o.ParameterList; body = (SyntaxNode?)o.Body ?? o.ExpressionBody; break;
                case AccessorDeclarationSyntax a when a.Body != null || a.ExpressionBody != null:
                    name = a.Keyword.Text; body = (SyntaxNode?)a.Body ?? a.ExpressionBody; break;
            }
            if (name == null || body == null) continue;
            var m2 = new Counter();
            if (body is BlockSyntax b) foreach (var s in b.Statements) m2.Visit(s, 0);
            else { m2.Statements = 1; m2.Visit(body, 0); }
            var span = n.GetLocation().GetLineSpan();
            yield return new JsonObject
            {
                ["path"] = path, ["symbol"] = Qualify(n, name),
                ["line"] = span.StartLinePosition.Line + 1, ["end_line"] = span.EndLinePosition.Line + 1,
                ["statements"] = m2.Statements, ["params"] = ps?.Parameters.Count ?? 0,
                ["nesting"] = m2.Nesting, ["cyclomatic"] = m2.Cyclomatic,
            };
        }
    }

    static string Qualify(SyntaxNode n, string name)
    {
        var parts = new List<string> { name };
        for (var p = n.Parent; p != null; p = p.Parent)
        {
            switch (p)
            {
                case BaseTypeDeclarationSyntax t: parts.Insert(0, t.Identifier.Text); break;
                case PropertyDeclarationSyntax pr: parts.Insert(0, pr.Identifier.Text); break;
                case IndexerDeclarationSyntax: parts.Insert(0, "this[]"); break;
                case EventDeclarationSyntax ev: parts.Insert(0, ev.Identifier.Text); break;
                case MethodDeclarationSyntax m: parts.Insert(0, m.Identifier.Text); break;
                case LocalFunctionStatementSyntax l: parts.Insert(0, l.Identifier.Text); break;
            }
        }
        return string.Join(".", parts);
    }

    sealed class Counter
    {
        public int Statements, Nesting, Cyclomatic = 1;

        public void Visit(SyntaxNode n, int depth)
        {
            if (n is StatementSyntax && n is not BlockSyntax && n is not EmptyStatementSyntax) Statements++;
            switch (n)
            {
                case LocalFunctionStatementSyntax:
                    return; // measured as its own unit
                case IfStatementSyntax i:
                    VisitIf(i, depth);
                    return;
                case ForStatementSyntax or ForEachStatementSyntax or WhileStatementSyntax or DoStatementSyntax:
                    Cyclomatic++;
                    Nest(n, depth);
                    return;
                case SwitchStatementSyntax or TryStatementSyntax:
                    Nest(n, depth);
                    return;
                case CaseSwitchLabelSyntax or CasePatternSwitchLabelSyntax:
                case CatchClauseSyntax:
                case ConditionalExpressionSyntax:
                    Cyclomatic++;
                    break;
                case SwitchExpressionArmSyntax arm when arm.Pattern is not DiscardPatternSyntax:
                    Cyclomatic++;
                    break;
                case BinaryExpressionSyntax be when be.IsKind(SyntaxKind.LogicalAndExpression) || be.IsKind(SyntaxKind.LogicalOrExpression) || be.IsKind(SyntaxKind.CoalesceExpression):
                    Cyclomatic++;
                    break;
            }
            foreach (var c in n.ChildNodes()) Visit(c, depth);
        }

        void Nest(SyntaxNode n, int depth)
        {
            Nesting = Math.Max(Nesting, depth + 1);
            foreach (var c in n.ChildNodes())
                Visit(c, c is StatementSyntax or SwitchSectionSyntax or CatchClauseSyntax or FinallyClauseSyntax ? depth + 1 : depth);
        }

        void VisitIf(IfStatementSyntax i, int depth)
        {
            Cyclomatic++;
            Nesting = Math.Max(Nesting, depth + 1);
            Visit(i.Condition, depth);
            Visit(i.Statement, depth + 1);
            if (i.Else == null) return;
            if (i.Else.Statement is IfStatementSyntax elif)
            {
                Statements++; // else-if continues the chain at the same depth
                VisitIf(elif, depth);
            }
            else Visit(i.Else.Statement, depth + 1);
        }
    }
}
