#!/bin/sh
# Fetches the benchmark corpus and the C reference parsers at pinned revisions
# and lays the corpus out as one directory per category.
# Usage: corpus.sh <workdir>
set -e
W=${1:?usage: corpus.sh <workdir>}
mkdir -p "$W" && cd "$W"

fetch() { # repo rev
  d=$(basename "$1")
  [ -d "$d" ] || git clone -q --filter=blob:none "https://github.com/$1" "$d"
  git -C "$d" checkout -q "$2"
}
fetch commonmark/commonmark-spec 3da939428d80f146f270cd1765e4ba462e96bb1b
fetch rust-lang/book 1500248d8f230566e4ec9f27fcbb8fe9e2898ab1
fetch kubernetes/website f613865074e00c06f7cc2414b1711e038564011d
fetch commonmark/cmark 6566d91bdb25898bd910f8bed2a3f18f4a05cf91
fetch mity/md4c 7fc1815a5eeba2af7d6120a76202bf59f3b6e6e4

rm -rf corpus && mkdir -p corpus/spec corpus/book corpus/k8sdocs corpus/k8sref
cp commonmark-spec/spec.txt corpus/spec/spec.md
find book/src -name '*.md' -exec cp {} corpus/book/ \;
i=0
find website/content/en/docs -name '*.md' | sort | while read -r f; do
  i=$((i + 1))
  case $f in
  */docs/reference/*) cp "$f" "corpus/k8sref/$i.md" ;;
  *) cp "$f" "corpus/k8sdocs/$i.md" ;;
  esac
done
