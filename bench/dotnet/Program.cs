// The benchmark runner for Markdig. See ../README.md for the protocol every
// runner follows.
using System.Buffers.Binary;
using System.Diagnostics;
using System.Text;
using Markdig;

const string Usage = "usage: dotnet <parser> render | <parser> bench <bundle> <secs>";

if (args.Length < 2 || args[0] != "markdig")
    Fail(Usage);
// The default pipeline is CommonMark without extensions, and passes raw HTML
// through. Reusing one StringWriter keeps its buffer across documents.
var pipeline = new MarkdownPipelineBuilder().Build();
var writer = new StringWriter();
string ToHtml(string src)
{
    writer.GetStringBuilder().Clear();
    Markdown.ToHtml(src, writer, pipeline);
    return writer.ToString();
}

if (args[1] == "render" && args.Length == 2)
    Render();
else if (args[1] == "bench" && args.Length == 4)
    Bench(args[2], double.Parse(args[3], System.Globalization.CultureInfo.InvariantCulture));
else
    Fail(Usage);

void Render()
{
    var stdin = Console.OpenStandardInput();
    var stdout = Console.OpenStandardOutput();
    var n = new byte[4];
    while (true)
    {
        try { stdin.ReadExactly(n); } catch (EndOfStreamException) { return; }
        var src = new byte[BinaryPrimitives.ReadUInt32LittleEndian(n)];
        stdin.ReadExactly(src);
        var outBytes = Encoding.UTF8.GetBytes(ToHtml(Encoding.UTF8.GetString(src)));
        BinaryPrimitives.WriteUInt32LittleEndian(n, (uint)outBytes.Length);
        stdout.Write(n);
        stdout.Write(outBytes);
        stdout.Flush();
    }
}

void Bench(string bundle, double secs)
{
    var b = File.ReadAllBytes(bundle);
    var docs = new List<string>();
    for (int i = 0; i + 4 <= b.Length;)
    {
        int n = (int)BinaryPrimitives.ReadUInt32LittleEndian(b.AsSpan(i));
        docs.Add(Encoding.UTF8.GetString(b, i + 4, n));
        i += 4 + n;
    }
    var round = TimeSpan.FromSeconds(secs / 6);
    double best = double.PositiveInfinity;
    long sink = 0;
    // The first round is warm-up and is not counted; it also gives tiered
    // compilation time to reach its optimised code.
    for (int r = 0; r < 6; r++)
    {
        int passes = 0;
        var sw = Stopwatch.StartNew();
        do
        {
            foreach (var d in docs)
                sink += ToHtml(d).Length;
            passes++;
        } while (sw.Elapsed < round);
        if (r > 0)
            best = Math.Min(best, sw.Elapsed.TotalSeconds / passes);
    }
    if (sink == 0)
        Fail("no output");
    Console.WriteLine(best.ToString("F9", System.Globalization.CultureInfo.InvariantCulture));
}

static void Fail(string msg)
{
    Console.Error.WriteLine(msg);
    Environment.Exit(2);
}
