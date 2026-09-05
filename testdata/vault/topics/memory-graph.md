---
type: topic
title: Memory graph
---
Edges in [[brain]] come from three sources with different trust levels:
wikilinks in note bodies (absolute), typed frontmatter relations (carry an
explicit confidence), and embedding similarity computed at view time.

Similarity edges are never persisted to disk — they are a lens, not a fact.
Rendering is ego-mode only, two hops from a focus node.
