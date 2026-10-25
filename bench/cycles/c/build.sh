#!/bin/sh
# Builds cbench against cmark and md4c source checkouts without cmake.
# Usage: build.sh <cmark-dir> <md4c-dir>
set -e
CMARK=$1 MD4C=$2 OUT=$(mktemp -d)
cat > "$OUT/cmark_export.h" <<'H'
#define CMARK_EXPORT
H
cat > "$OUT/cmark_version.h" <<'H'
#define CMARK_VERSION 0
#define CMARK_VERSION_STRING "bench"
H
cat > "$OUT/config.h" <<'H'
#include <stdbool.h>
#define HAVE___BUILTIN_EXPECT 1
#define HAVE___ATTRIBUTE__ 1
#define CMARK_ATTRIBUTE(list) __attribute__ (list)
#define CMARK_INLINE inline
H
SRCS=$(ls "$CMARK"/src/*.c | grep -v '/main.c$')
cc -O3 -DNDEBUG -I"$OUT" -I"$CMARK/src" -I"$MD4C/src" -o "$(dirname "$0")/cbench" \
  "$(dirname "$0")/cbench.c" $SRCS "$MD4C/src/md4c.c"
