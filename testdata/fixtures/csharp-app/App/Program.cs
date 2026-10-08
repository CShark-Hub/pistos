using System.Text.RegularExpressions;

static partial class Program
{
    [GeneratedRegex("^[a-z]+$")]
    private static partial Regex Word();

    static void Main(string[] args)
    {
        var name = args.Length > 0 && Word().IsMatch(args[0]) ? args[0] : "world";
        Console.WriteLine(Lib.Greeter.Greet(name));
    }
}
