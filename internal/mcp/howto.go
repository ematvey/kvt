package mcp

func DefaultInstructions() string {
	return "Use kvt_search before writing when the path is uncertain. Read with kvt_read before edits, pass base_hash on write/edit/delete for conflict detection, and use kvt_howto for detailed KVT workflow guidance."
}

func DefaultHowto() string {
	return `# KVT Howto

Use ` + "`kvt_search`" + ` before writing unless you already know the exact path. Prefer ` + "`kvt_read`" + ` before edits so you have the current content and ` + "`base_hash`" + `.

Write normalized bundle-relative markdown paths such as ` + "`people/alice.md`" + `. Do not write outside the vault, do not create service-owned ` + "`index.md`" + ` files, and let KVT manage the ` + "`timestamp`" + ` frontmatter field.

Every concept needs frontmatter with at least ` + "`type`" + `. Include stable ` + "`title`" + ` and domain fields from the ontology. Use markdown links for body relationships and frontmatter refs for typed links.

On conflicts, read the document again, merge your intended change, and retry with the new ` + "`base_hash`" + `. Use ` + "`kvt_validate`" + ` before committing or reporting that a vault edit is complete.
`
}
