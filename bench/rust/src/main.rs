//! The benchmark runner for the Rust parsers: pulldown-cmark, comrak and
//! ox-content. See ../README.md for the protocol every runner follows.

use std::io::{Read, Write};
use std::time::{Duration, Instant};

use ox_content_allocator::Allocator;
use ox_content_parser::{Parser, ParserOptions};
use ox_content_renderer::{HtmlRenderer, HtmlRendererOptions};

const USAGE: &str = "usage: rust <parser> render | <parser> bench <bundle> <secs>";

/// Renders documents one after another, reusing whatever memory the parser's
/// API lets a caller reuse. `to_html` returns the HTML, valid until the next
/// call.
trait ToHtml {
    fn to_html(&mut self, src: &str) -> &str;
}

struct Pulldown(String);

impl ToHtml for Pulldown {
    fn to_html(&mut self, src: &str) -> &str {
        self.0.clear();
        let parser = pulldown_cmark::Parser::new_ext(src, pulldown_cmark::Options::empty());
        pulldown_cmark::html::push_html(&mut self.0, parser);
        &self.0
    }
}

struct Comrak(comrak::Options<'static>, String);

impl ToHtml for Comrak {
    fn to_html(&mut self, src: &str) -> &str {
        self.1 = comrak::markdown_to_html(src, &self.0);
        &self.1
    }
}

/// ox-content keeps an arena and a renderer across calls the way its own
/// Node binding does (ox_content_napi/src/render_scratch.rs).
struct OxContent {
    allocator: Allocator,
    renderer: HtmlRenderer,
}

impl ToHtml for OxContent {
    fn to_html(&mut self, src: &str) -> &str {
        if self.allocator.allocated_bytes() < Allocator::capacity_for_source_len(src.len()) {
            self.allocator = Allocator::for_source_len(src.len());
        } else {
            self.allocator.reset();
        }
        let doc = Parser::with_options(&self.allocator, src, ParserOptions::default())
            .parse()
            .expect("ox-content parse failed");
        self.renderer.render_borrowed(&doc)
    }
}

fn ox_content() -> OxContent {
    // The defaults add links and targets that CommonMark does not have.
    let mut opts = HtmlRendererOptions::new();
    opts.xhtml = true;
    opts.hard_break = "<br />\n".to_string();
    opts.autolink_urls = false;
    opts.autolink_target_blank = false;
    opts.link_target_blank = false;
    OxContent { allocator: Allocator::new(), renderer: HtmlRenderer::with_options(opts) }
}

fn comrak() -> Comrak {
    let mut opts = comrak::Options::default();
    opts.render.r#unsafe = true;
    Comrak(opts, String::new())
}

fn main() {
    let args: Vec<String> = std::env::args().collect();
    if args.len() < 3 {
        fail(USAGE);
    }
    let mut p: Box<dyn ToHtml> = match args[1].as_str() {
        "pulldown-cmark" => Box::new(Pulldown(String::new())),
        "comrak" => Box::new(comrak()),
        "ox-content" => Box::new(ox_content()),
        _ => fail("unknown parser"),
    };
    match (args[2].as_str(), args.len()) {
        ("render", 3) => render(p.as_mut()),
        ("bench", 5) => bench(p.as_mut(), &args[3], args[4].parse().expect("secs")),
        _ => fail(USAGE),
    }
}

fn render(p: &mut dyn ToHtml) {
    let mut stdin = std::io::stdin().lock();
    let mut stdout = std::io::stdout().lock();
    let mut n = [0u8; 4];
    while stdin.read_exact(&mut n).is_ok() {
        let mut src = vec![0; u32::from_le_bytes(n) as usize];
        stdin.read_exact(&mut src).expect("short read");
        let src = String::from_utf8_lossy(&src);
        let out = p.to_html(&src);
        stdout.write_all(&(out.len() as u32).to_le_bytes()).unwrap();
        stdout.write_all(out.as_bytes()).unwrap();
        stdout.flush().unwrap();
    }
}

fn bench(p: &mut dyn ToHtml, bundle: &str, secs: f64) {
    let b = std::fs::read(bundle).expect("read bundle");
    let mut docs = Vec::new();
    let mut i = 0;
    while i + 4 <= b.len() {
        let n = u32::from_le_bytes(b[i..i + 4].try_into().unwrap()) as usize;
        docs.push(String::from_utf8_lossy(&b[i + 4..i + 4 + n]).into_owned());
        i += 4 + n;
    }
    let round = Duration::from_secs_f64(secs / 6.0);
    let mut best = f64::INFINITY;
    let mut sink = 0;
    // The first round is warm-up and is not counted.
    for r in 0..6 {
        let mut passes = 0u32;
        let start = Instant::now();
        let mut el;
        loop {
            for d in &docs {
                sink += p.to_html(d).len();
            }
            passes += 1;
            el = start.elapsed();
            if el >= round {
                break;
            }
        }
        if r > 0 {
            best = best.min(el.as_secs_f64() / passes as f64);
        }
    }
    if sink == 0 {
        fail("no output");
    }
    println!("{best:.9}");
}

fn fail(msg: &str) -> ! {
    eprintln!("{msg}");
    std::process::exit(2);
}
