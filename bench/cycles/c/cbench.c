// cbench measures parse-only throughput of cmark and md4c on a directory of
// markdown files. For cmark it splits time between block parsing
// (cmark_parser_feed) and inline parsing plus finalisation
// (cmark_parser_finish). Rendering is never timed.
//
// It also times a NEON stage-1 kernel (simdjson-style 64-byte bitmap of
// newlines and CommonMark inline specials) over the concatenated corpus, as
// the ceiling for what a vectorised byte scan costs.
//
// Usage: cbench <dir> [min-seconds]
#include <dirent.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

#include <arm_neon.h>
#include <stdint.h>

#include "cmark.h"
#include "md4c.h"

typedef struct {
  char *data;
  size_t len;
} doc;

static double now(void) {
  struct timespec ts;
  clock_gettime(CLOCK_MONOTONIC_RAW, &ts);
  return ts.tv_sec + ts.tv_nsec * 1e-9;
}

static int noop_block(MD_BLOCKTYPE t, void *d, void *u) { return 0; }
static int noop_span(MD_SPANTYPE t, void *d, void *u) { return 0; }
static int noop_text(MD_TEXTTYPE t, const MD_CHAR *s, MD_SIZE n, void *u) {
  return 0;
}

// neon_bitmap writes one bit per input byte to out, set for \n ` * _ [ ] ! <
// \\ and &, and returns the number of set bits.
static size_t neon_bitmap(const uint8_t *b, size_t n, uint64_t *out) {
  static const char specials[] = "\n`*_[]!<\\&";
  const uint8x16_t bitsel = {1, 2, 4, 8, 16, 32, 64, 128,
                             1, 2, 4, 8, 16, 32, 64, 128};
  uint8x16_t targets[10];
  for (int k = 0; k < 10; k++)
    targets[k] = vdupq_n_u8(specials[k]);
  size_t count = 0, w = 0, i = 0;
  for (; i + 64 <= n; i += 64, w++) {
    uint8x16_t m[4];
    for (int j = 0; j < 4; j++) {
      uint8x16_t x = vld1q_u8(b + i + 16 * j);
      uint8x16_t acc = vceqq_u8(x, targets[0]);
      for (int k = 1; k < 10; k++)
        acc = vorrq_u8(acc, vceqq_u8(x, targets[k]));
      m[j] = vandq_u8(acc, bitsel);
    }
    uint8x16_t s0 = vpaddq_u8(m[0], m[1]);
    uint8x16_t s1 = vpaddq_u8(m[2], m[3]);
    s0 = vpaddq_u8(s0, s1);
    s0 = vpaddq_u8(s0, s0);
    uint64_t bits = vgetq_lane_u64(vreinterpretq_u64_u8(s0), 0);
    out[w] = bits;
    count += __builtin_popcountll(bits);
  }
  uint64_t tail = 0;
  for (size_t j = 0; i + j < n; j++)
    if (memchr(specials, b[i + j], 10) && b[i + j])
      tail |= 1ull << j;
  if (i < n)
    out[w] = tail, count += __builtin_popcountll(tail);
  return count;
}

int main(int argc, char **argv) {
  if (argc < 2) {
    fprintf(stderr, "usage: cbench <dir> [min-seconds]\n");
    return 2;
  }
  double minSecs = argc > 2 ? atof(argv[2]) : 2.0;

  doc *docs = NULL;
  size_t ndocs = 0, total = 0;
  DIR *dir = opendir(argv[1]);
  if (!dir) {
    perror(argv[1]);
    return 1;
  }
  struct dirent *e;
  while ((e = readdir(dir))) {
    if (e->d_name[0] == '.')
      continue;
    char path[4096];
    snprintf(path, sizeof path, "%s/%s", argv[1], e->d_name);
    FILE *f = fopen(path, "rb");
    if (!f)
      continue;
    fseek(f, 0, SEEK_END);
    long n = ftell(f);
    fseek(f, 0, SEEK_SET);
    docs = realloc(docs, (ndocs + 1) * sizeof(doc));
    docs[ndocs].data = malloc(n);
    docs[ndocs].len = fread(docs[ndocs].data, 1, n, f);
    total += docs[ndocs].len;
    ndocs++;
    fclose(f);
  }
  closedir(dir);

  MD_PARSER md = {0};
  md.flags = MD_DIALECT_COMMONMARK;
  md.enter_block = noop_block;
  md.leave_block = noop_block;
  md.enter_span = noop_span;
  md.leave_span = noop_span;
  md.text = noop_text;

  // Every measurement takes the best of ROUNDS rounds to filter out
  // interference from other processes and efficiency-core scheduling.
  enum { ROUNDS = 5 };
  double roundSecs = minSecs / ROUNDS;

  // md4c
  size_t iters;
  double start, el, best = 0;
  for (int r = 0; r < ROUNDS; r++) {
    iters = 0;
    start = now();
    do {
      for (size_t i = 0; i < ndocs; i++)
        md_parse(docs[i].data, docs[i].len, &md, NULL);
      iters++;
    } while ((el = now() - start) < roundSecs);
    if (total * iters / el > best)
      best = total * iters / el;
  }
  printf("md4c  total %.1f MB/s\n", best / 1e6);

  // cmark, split into block (feed) and inline (finish) phases
  double bFeed = 0, bFinish = 0, bFree = 0;
  best = 0;
  for (int r = 0; r < ROUNDS; r++) {
    iters = 0;
    double tFeed = 0, tFinish = 0, tFree = 0;
    start = now();
    do {
      for (size_t i = 0; i < ndocs; i++) {
        double t0 = now();
        cmark_parser *p = cmark_parser_new(CMARK_OPT_DEFAULT);
        cmark_parser_feed(p, docs[i].data, docs[i].len);
        double t1 = now();
        cmark_node *root = cmark_parser_finish(p);
        double t2 = now();
        cmark_parser_free(p);
        cmark_node_free(root);
        tFree += now() - t2;
        tFeed += t1 - t0;
        tFinish += t2 - t1;
      }
      iters++;
    } while (now() - start < roundSecs);
    double all = tFeed + tFinish + tFree;
    if (total * iters / all > best) {
      best = total * iters / all;
      bFeed = tFeed / all, bFinish = tFinish / all, bFree = tFree / all;
    }
  }
  printf("cmark total %.1f MB/s  blocks %.0f%%  inlines+finalize %.0f%%  "
         "free %.0f%%\n",
         best / 1e6, 100 * bFeed, 100 * bFinish, 100 * bFree);

  // NEON stage-1 kernel over the concatenated corpus
  uint8_t *joined = malloc(total);
  uint64_t *bitmap = malloc((total + 63) / 64 * 8);
  for (size_t i = 0, off = 0; i < ndocs; off += docs[i].len, i++)
    memcpy(joined + off, docs[i].data, docs[i].len);
  size_t expect = 0;
  for (size_t i = 0; i < total; i++)
    expect += joined[i] && memchr("\n`*_[]!<\\&", joined[i], 10) != NULL;
  volatile size_t sink = 0;
  best = 0;
  for (int r = 0; r < ROUNDS; r++) {
    iters = 0;
    start = now();
    do {
      sink = neon_bitmap(joined, total, bitmap);
      iters++;
    } while ((el = now() - start) < roundSecs);
    if (total * iters / el > best)
      best = total * iters / el;
  }
  if (sink != expect) {
    fprintf(stderr, "neon bitmap mismatch: %zu vs %zu\n", (size_t)sink, expect);
    return 1;
  }
  printf("neon  stage-1 bitmap %.1f MB/s\n", best / 1e6);
  return 0;
}
