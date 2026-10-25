// The benchmark runner for the C parsers, cmark and md4c. See ../README.md
// for the protocol every runner follows.
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

#include "cmark.h"
#include "md4c-html.h"

typedef struct {
  char *data;
  size_t len, cap;
} buf;

static double now(void) {
  struct timespec ts;
  clock_gettime(CLOCK_MONOTONIC_RAW, &ts);
  return ts.tv_sec + ts.tv_nsec * 1e-9;
}

static void fail(const char *msg) {
  fprintf(stderr, "%s\n", msg);
  exit(2);
}

static void append(const MD_CHAR *s, MD_SIZE n, void *u) {
  buf *b = u;
  if (b->len + n > b->cap) {
    b->cap = 2 * (b->len + n);
    b->data = realloc(b->data, b->cap);
  }
  memcpy(b->data + b->len, s, n);
  b->len += n;
}

// to_html renders src into out, reusing out's memory, as a caller rendering
// many documents would.
typedef void (*to_html_fn)(const char *src, size_t n, buf *out);

static void cmark_to_html(const char *src, size_t n, buf *out) {
  free(out->data);
  out->data = cmark_markdown_to_html(src, n, CMARK_OPT_UNSAFE);
  out->len = strlen(out->data);
}

// md4c streams its output through a callback; collecting it in a buffer that
// keeps its capacity across documents is what md4c's own md2html does.
static void md4c_to_html(const char *src, size_t n, buf *out) {
  out->len = 0;
  if (md_html(src, n, append, out, MD_DIALECT_COMMONMARK, MD_HTML_FLAG_XHTML))
    fail("md_html failed");
}

static uint32_t get32(const unsigned char *p) {
  return p[0] | p[1] << 8 | p[2] << 16 | (uint32_t)p[3] << 24;
}

static void render(to_html_fn to_html) {
  unsigned char n[4];
  buf out = {0};
  while (fread(n, 1, 4, stdin) == 4) {
    uint32_t len = get32(n);
    char *src = malloc(len + 1);
    if (fread(src, 1, len, stdin) != len)
      fail("short read");
    to_html(src, len, &out);
    uint32_t m = out.len;
    unsigned char h[4] = {m, m >> 8, m >> 16, m >> 24};
    fwrite(h, 1, 4, stdout);
    fwrite(out.data, 1, out.len, stdout);
    fflush(stdout);
    free(src);
  }
}

static void bench(to_html_fn to_html, const char *path, double secs) {
  FILE *f = fopen(path, "rb");
  if (!f)
    fail(path);
  fseek(f, 0, SEEK_END);
  long size = ftell(f);
  fseek(f, 0, SEEK_SET);
  unsigned char *all = malloc(size);
  if (fread(all, 1, size, f) != (size_t)size)
    fail("short read");
  fclose(f);
  size_t ndocs = 0;
  buf *docs = NULL;
  for (long i = 0; i + 4 <= size;) {
    uint32_t n = get32(all + i);
    docs = realloc(docs, (ndocs + 1) * sizeof *docs);
    docs[ndocs++] = (buf){(char *)all + i + 4, n, n};
    i += 4 + n;
  }

  double round = secs / 6, best = 1e300;
  size_t sink = 0;
  buf out = {0};
  // The first round is warm-up and is not counted.
  for (int r = 0; r < 6; r++) {
    size_t passes = 0;
    double start = now(), el;
    do {
      for (size_t i = 0; i < ndocs; i++) {
        to_html(docs[i].data, docs[i].len, &out);
        sink += out.len;
      }
      passes++;
    } while ((el = now() - start) < round);
    if (r > 0 && el / passes < best)
      best = el / passes;
  }
  if (!sink)
    fail("no output");
  printf("%.9f\n", best);
}

int main(int argc, char **argv) {
  if (argc < 3)
    fail("usage: c <parser> render | <parser> bench <bundle> <secs>");
  to_html_fn to_html;
  if (!strcmp(argv[1], "cmark"))
    to_html = cmark_to_html;
  else if (!strcmp(argv[1], "md4c"))
    to_html = md4c_to_html;
  else
    fail("unknown parser");
  if (!strcmp(argv[2], "render"))
    render(to_html);
  else if (!strcmp(argv[2], "bench") && argc == 5)
    bench(to_html, argv[3], atof(argv[4]));
  else
    fail("usage: c <parser> render | <parser> bench <bundle> <secs>");
  return 0;
}
