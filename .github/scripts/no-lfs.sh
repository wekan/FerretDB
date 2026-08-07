#!/usr/bin/env bash
#
# no-lfs.sh - fail if anything in this repository is stored in Git LFS.
#
# This fork does not use Git LFS, and cannot: LFS in a fork network is billed to
# the ROOT repository's owner, which is FerretDB/FerretDB, not wekan. When that
# budget ran out, `git clone git@github.com:wekan/FerretDB` transferred every
# object and then died in checkout - "Smudge error: ... This repository exceeded
# its LFS budget" - and there was no button in the wekan organisation to press,
# because the account responsible is upstream's. The fix was to stop using LFS
# at all: .gitattributes keeps `-text` on the image patterns so they stay binary
# to Git, but names no filter, and the one image the fork carries is an ordinary
# blob. This script keeps it that way, because the way it comes back is quiet -
# a merge from upstream, a `git lfs track`, an image pasted in by a tool that
# runs `git lfs install` first - and the symptom appears in somebody else's
# clone, not in the commit that caused it.
#
# It checks three things:
#
#   1. no tracked file is an LFS pointer,
#   2. no .gitattributes routes a pattern through the lfs filter,
#   3. no .lfsconfig exists.
#
# Run it anywhere: ./.github/scripts/no-lfs.sh, or ./build.sh no-lfs, or menu
# entry 8 (Lint / vet), which calls it before vetting.

set -uo pipefail

cd "$(git rev-parse --show-toplevel)" || exit 2

# The first line of a pointer file, from the Git LFS spec.
LFS_MAGIC="version https://git-lfs.github.com/spec/v1"

fail=0
note() { printf '%s\n' "$*" >&2; }

# Findings are newline-separated strings rather than arrays on purpose: an empty
# array under `set -u` is an error in bash 3.2, which is what macOS still ships,
# and this script runs from build.sh there too. No path in this repository
# contains a newline, and `git ls-files -z` keeps that assumption checkable.

# ---- 1. tracked files that are LFS pointers --------------------------------
#
# Deliberately NOT `git grep -I`: a path whose `diff` attribute is unset counts
# as binary to grep, and `-I` then skips it - so the day someone writes the
# usual `*.png binary` macro (which is `-diff -merge -text`) into .gitattributes,
# a grep-based guard silently stops looking at exactly the paths a pointer is
# most likely to appear on. Verified: with `*.png binary`, `git grep -I` misses
# a pointer this loop still finds. With today's plain `-text` attributes grep
# would work, and that is the point - the guard must not depend on an attribute
# line that is not its own.
#
# Reading the first line is exact instead. A pointer is ~130 bytes (version, oid,
# size), so anything over 1 KiB cannot be one and is never opened - which is also
# why this script and the workflow that runs it, both of which quote the magic
# string, are not false positives: they are too big, not specially excepted.
pointers=""
while IFS= read -r -d '' f; do
  [ -f "$f" ] || continue                       # submodule, or deleted in the tree
  size=$(wc -c < "$f" 2>/dev/null) || continue
  [ "$size" -le 1024 ] || continue
  IFS= read -r first < "$f" 2>/dev/null || continue
  [ "$first" = "$LFS_MAGIC" ] && pointers="$pointers$f
"
done < <(git ls-files -z)

if [ -n "$pointers" ]; then
  fail=1
  note "ERROR: these files are Git LFS pointers, not their real contents:"
  printf '         %s\n' $pointers >&2
  note ""
  note "       Every clone of this repository would fail its checkout on them,"
  note "       because the LFS budget belongs to FerretDB/FerretDB upstream."
  note "       Commit the real bytes instead:"
  note ""
  note "         git rm --cached <file>                 # drop the pointer"
  note "         cp /path/to/real/<file> <file>         # the actual contents"
  note "         git add <file> && git commit"
  note ""
  note "       Check the result with: git lfs status  (it must report no LFS objects)."
fi

# ---- 2. .gitattributes routing anything through LFS ------------------------
#
# Comments are ignored: a commented-out `filter=lfs` line is inert, and keeping
# one as a note of what this repository does not do is legitimate.
attrs=""
while IFS= read -r -d '' f; do
  [ -f "$f" ] || continue
  grep -v '^[[:space:]]*#' "$f" 2>/dev/null | grep -q 'filter=lfs' && attrs="$attrs$f
"
done < <(git ls-files -z -- '.gitattributes' '**/.gitattributes')

if [ -n "$attrs" ]; then
  fail=1
  note "ERROR: these .gitattributes files send paths through the LFS filter:"
  for f in $attrs; do
    note "         $f"
    grep -n '' "$f" | grep -v ':[[:space:]]*#' | grep 'filter=lfs' | sed 's/^/           /' >&2
  done
  note ""
  note "       Keep the '-text' part, which marks the pattern binary so Git does"
  note "       no end-of-line conversion, and drop the filter/diff/merge part:"
  note ""
  note "         *.png -text            # not: *.png filter=lfs diff=lfs merge=lfs -text"
fi

# ---- 3. .lfsconfig ---------------------------------------------------------
lfsconfigs=""
while IFS= read -r -d '' f; do lfsconfigs="$lfsconfigs$f
"; done < <(git ls-files -z -- '.lfsconfig' '**/.lfsconfig')

if [ -n "$lfsconfigs" ]; then
  fail=1
  note "ERROR: an .lfsconfig is committed, so something here expects Git LFS:"
  printf '         %s\n' $lfsconfigs >&2
  note "       Remove it, and commit any LFS-stored file as an ordinary blob."
fi

if [ "$fail" -ne 0 ]; then
  note ""
  note "See CHANGELOG.md - \"Cloning the repository works again without a Git LFS"
  note "budget\" - for why this fork stores no files in Git LFS."
  exit 1
fi

printf 'no-lfs: OK - no LFS pointers, no lfs filter in .gitattributes, no .lfsconfig.\n'
