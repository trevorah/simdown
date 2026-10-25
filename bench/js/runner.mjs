// The benchmark runner for the JavaScript parsers: markdown-it, marked and
// micromark. See ../README.md for the protocol every runner follows.
import { readFileSync, readSync, writeSync } from 'node:fs';
import markdownit from 'markdown-it';
import { Marked } from 'marked';
import { micromark } from 'micromark';

const usage = 'usage: js <parser> render | <parser> bench <bundle> <secs>';

function fail(msg) {
  process.stderr.write(msg + '\n');
  process.exit(2);
}

const [name, mode, ...rest] = process.argv.slice(2);
let toHTML;
switch (name) {
  case 'markdown-it': {
    // The commonmark preset is strict CommonMark, raw HTML included.
    const md = markdownit('commonmark');
    toHTML = (src) => md.render(src);
    break;
  }
  case 'marked': {
    // marked defaults to GFM; turn it off for CommonMark.
    const m = new Marked({ gfm: false });
    toHTML = (src) => m.parse(src);
    break;
  }
  case 'micromark': {
    const opts = { allowDangerousHtml: true, allowDangerousProtocol: true };
    toHTML = (src) => micromark(src, opts);
    break;
  }
  default:
    fail(usage);
}

function readExactly(n) {
  const b = Buffer.alloc(n);
  for (let off = 0; off < n; ) {
    const got = readSync(0, b, off, n - off, null);
    if (got === 0) return null;
    off += got;
  }
  return b;
}

function render() {
  for (;;) {
    const h = readExactly(4);
    if (h === null) return;
    const src = readExactly(h.readUInt32LE(0)) ?? Buffer.alloc(0);
    const out = Buffer.from(toHTML(src.toString('utf8')), 'utf8');
    const n = Buffer.alloc(4);
    n.writeUInt32LE(out.length, 0);
    writeSync(1, n);
    writeSync(1, out);
  }
}

function bench(bundle, secs) {
  const b = readFileSync(bundle);
  const docs = [];
  for (let i = 0; i + 4 <= b.length; ) {
    const n = b.readUInt32LE(i);
    docs.push(b.toString('utf8', i + 4, i + 4 + n));
    i += 4 + n;
  }
  const round = (secs / 6) * 1e9;
  let best = Infinity;
  let sink = 0;
  // The first round is warm-up and is not counted; it also gives the JIT
  // time to reach its optimised code.
  for (let r = 0; r < 6; r++) {
    let passes = 0;
    const start = process.hrtime.bigint();
    let el;
    do {
      for (const d of docs) sink += toHTML(d).length;
      passes++;
      el = Number(process.hrtime.bigint() - start);
    } while (el < round);
    if (r > 0) best = Math.min(best, el / 1e9 / passes);
  }
  if (sink === 0) fail('no output');
  process.stdout.write(best.toFixed(9) + '\n');
}

if (mode === 'render' && rest.length === 0) render();
else if (mode === 'bench' && rest.length === 2) bench(rest[0], Number(rest[1]));
else fail(usage);
