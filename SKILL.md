# KVT Vault Authoring

Use this guidance when editing a KVT vault directly from a checkout.

- Write bundle-relative markdown paths such as `people/alice.md`; do not use absolute paths, `..`, or hidden runtime directories.
- Every concept document needs YAML frontmatter with at least `type`; include a stable `title` and ontology fields when they apply.
- Do not hand-author service-owned `index.md` files. KVT regenerates them.
- Do not rely on a handwritten `timestamp`; KVT write/edit tools own that field.
- Use markdown links for body references and frontmatter refs for typed ontology relationships.
- Consult vault-specific house rules in `_howto.md` when present; through MCP, prefer `kvt_howto` for the same guidance.
- Read before editing and preserve the current `base_hash` when using KVT tools so conflicts can be detected.
- Search first with `kvt_search` when the target path is uncertain; use `kvt_grep` only for known exact text.
- Run `kvt validate --vault <path>` before committing vault edits.
