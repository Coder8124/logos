---
type: project
title: brain
aliases: [second brain, the copilot]
relations:
  - { pred: uses, obj: "[[ollama]]", conf: 1.0, src: stated }
  - { pred: owned_by, obj: "[[pragun]]", conf: 1.0, src: stated }
---
A local-first second brain. Watches what I do on my machine, distills it into
an Obsidian vault, and renders a memory graph in a menubar widget.

Core rule: markdown is truth, SQLite is a rebuildable cache. Retrieval is
brute-force cosine over note embeddings plus one hop along stated edges.

Currently at step 1 of the build order — indexing and retrieval only, no
capture layer yet.
