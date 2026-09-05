<!-- Not committed as vault content — this is a note to a human reading the tree. -->

# testdata/vault

A tiny fixture vault: four notes and the directory layout a real vault uses.
Tests and manual walkthroughs point at it when they need *a* vault that exists
and has some texture, without depending on anything on the machine.

It is **not** a live vault, and nothing writes to it. Yours is `$BRAIN_VAULT`
(default `~/brain`); a throwaway one is `BRAIN_VAULT=$(mktemp -d)`. A live vault
that happens to sit at `./vault` is gitignored precisely so it can never be
confused with this one.

`testdata/` is a name the Go toolchain skips, so this never ends up in a build.
