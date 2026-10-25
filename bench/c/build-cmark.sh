#!/bin/sh
# Builds the cmark CLI, for TestCorpusMatchesCmark, from a cmark source
# checkout without cmake.
# Usage: build-cmark.sh <cmark-dir> <out-dir>
set -e
CMARK=$1 OUT=$2 GEN=$(mktemp -d)
mkdir -p "$OUT"
cat > "$GEN/cmark_export.h" <<'H'
#define CMARK_EXPORT
H
cat > "$GEN/cmark_version.h" <<'H'
#define CMARK_VERSION 0
#define CMARK_VERSION_STRING "simdown"
H
cat > "$GEN/config.h" <<'H'
#include <stdbool.h>
#define HAVE___BUILTIN_EXPECT 1
#define HAVE___ATTRIBUTE__ 1
#define CMARK_ATTRIBUTE(list) __attribute__ (list)
#define CMARK_INLINE inline
H
cc -O2 -DNDEBUG -I"$GEN" -I"$CMARK/src" -o "$OUT/cmark" "$CMARK"/src/*.c
