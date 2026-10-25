#!/bin/sh
# Fetches and builds a benchmark runner for every parser the driver knows, at
# pinned versions, into <workdir>. A runner whose toolchain is missing is
# skipped with a message; the driver then skips that parser.
# Usage: build.sh <workdir>
set -e
W=${1:?usage: build.sh <workdir>}
HERE=$(cd "$(dirname "$0")" && pwd)
mkdir -p "$W/src"
W=$(cd "$W" && pwd)
have() { command -v "$1" >/dev/null 2>&1 || { echo "skipping $2: no $1" >&2; return 1; }; }

# Go: simdown and goldmark. GOEXPERIMENT=simd builds simdown's NEON code;
# the second build gives simdown's scalar twins, for comparison.
GOEXPERIMENT=simd go -C "$HERE" build -o "$W/go-runner" ./go
GOEXPERIMENT= go -C "$HERE" build -o "$W/go-runner-scalar" ./go

# C: cmark and md4c, at the revisions corpus.sh uses.
fetch() { # repo rev
  d="$W/src/$(basename "$1")"
  [ -d "$d" ] || git clone -q --filter=blob:none "https://github.com/$1" "$d"
  git -C "$d" checkout -q "$2"
}
fetch commonmark/cmark 6566d91bdb25898bd910f8bed2a3f18f4a05cf91
fetch mity/md4c 7fc1815a5eeba2af7d6120a76202bf59f3b6e6e4
GEN=$(mktemp -d)
cat > "$GEN/cmark_export.h" <<'H'
#define CMARK_EXPORT
H
cat > "$GEN/cmark_version.h" <<'H'
#define CMARK_VERSION 0
#define CMARK_VERSION_STRING "bench"
H
cat > "$GEN/config.h" <<'H'
#include <stdbool.h>
#define HAVE___BUILTIN_EXPECT 1
#define HAVE___ATTRIBUTE__ 1
#define CMARK_ATTRIBUTE(list) __attribute__ (list)
#define CMARK_INLINE inline
H
CMARK=$W/src/cmark MD4C=$W/src/md4c
cc -O3 -DNDEBUG -I"$GEN" -I"$CMARK/src" -I"$MD4C/src" -o "$W/c-runner" \
  "$HERE/c/runner.c" $(ls "$CMARK"/src/*.c | grep -v '/main.c$') \
  "$MD4C/src/md4c.c" "$MD4C/src/md4c-html.c" "$MD4C/src/entity.c"

# Rust: pulldown-cmark, comrak and ox-content, pinned by Cargo.lock.
if have cargo "pulldown-cmark, comrak, ox-content"; then
  cargo build -q --release --locked --manifest-path "$HERE/rust/Cargo.toml" --target-dir "$W/rust"
  cp "$W/rust/release/runner" "$W/rust-runner"
fi

# .NET: Markdig.
if have dotnet markdig; then
  # SDK 10.0.402 garbles an --artifacts-path whose last element is "dotnet".
  DOTNET_CLI_TELEMETRY_OPTOUT=1 DOTNET_NOLOGO=1 dotnet publish "$HERE/dotnet/runner.csproj" \
    -v q -c Release --artifacts-path "$W/dotnet-build"
  rm -rf "$W/dotnet-runner" && cp -R "$W/dotnet-build/publish/runner/release" "$W/dotnet-runner"
fi

# JavaScript: markdown-it, marked and micromark, pinned by package-lock.json.
if have node "markdown-it, marked, micromark"; then
  mkdir -p "$W/js"
  cp "$HERE/js/package.json" "$HERE/js/package-lock.json" "$HERE/js/runner.mjs" "$W/js/"
  npm ci --silent --prefix "$W/js"
fi
