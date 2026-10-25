// Renders markdown with commonmark.js for FuzzMatchesCommonmarkJS, one
// document after another over a pipe, so a fuzz worker does not start a
// process per input. Each request and each reply is a 4-byte little-endian
// length followed by that many bytes of UTF-8.
const cm = require('commonmark');

const parser = new cm.Parser();
const renderer = new cm.HtmlRenderer();
let buf = Buffer.alloc(0);

process.stdin.on('data', (chunk) => {
  buf = Buffer.concat([buf, chunk]);
  while (buf.length >= 4) {
    const n = buf.readUInt32LE(0);
    if (buf.length < 4 + n) {
      break;
    }
    const src = buf.toString('utf8', 4, 4 + n);
    buf = buf.subarray(4 + n);
    const html = Buffer.from(renderer.render(parser.parse(src)), 'utf8');
    const h = Buffer.alloc(4);
    h.writeUInt32LE(html.length, 0);
    process.stdout.write(Buffer.concat([h, html]));
  }
});
