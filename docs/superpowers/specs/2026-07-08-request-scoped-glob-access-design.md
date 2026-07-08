# Request-Scoped Glob Access Controls

Date: 2026-07-08

## Goal

Add request-scoped access controls to both REST and MCP so a caller can
restrict which vault paths an operation may read or mutate. Policies are
expressed as glob patterns over normalized bundle-relative paths.

The initial implementation is a request sandbox: the caller supplies an
`access` object that can only narrow the operation being performed. It is
not a complete security boundary when untrusted clients are allowed to
choose their own policy. The design keeps the policy model reusable so a
future authenticated API-key/session policy can be intersected with the
request policy without changing service operations again.

## Policy Shape

REST JSON bodies and MCP tool inputs accept:

```json
{
  "access": {
    "read_globs": ["**"],
    "write_globs": ["drafts/**", "notes/*.md"],
    "deny_globs": ["secrets/**"]
  }
}
```

REST GET routes use repeated query params:

```text
?read_glob=public/**&read_glob=notes/*.md&deny_glob=secrets/**
```

Field meanings:

- `read_globs`: allow read-like operations against matching paths.
- `write_globs`: allow mutating operations against matching paths.
- `deny_globs`: deny matching paths for both reads and writes; deny
  wins over allow.

Missing `access` means current behavior: unrestricted access to all
valid concept paths in the vault. Present but empty `access` means no
read or write access unless a relevant allow glob is provided.

## Glob Semantics

Patterns match normalized bundle-relative paths such as
`notes/a.md`.

- `*` matches zero or more non-slash characters inside one segment.
- `?` matches one non-slash character inside one segment.
- `**` matches across path separators.
- Matching is exact over the full path, not substring-based.
- Patterns are normalized to forward slashes and must be relative.
- Invalid patterns are rejected with a bad request error at the API
  boundary or with a structured tool error through MCP.

Examples:

- `notes/*.md` matches `notes/a.md`, not `notes/archive/a.md`.
- `notes/**` matches `notes/a.md` and `notes/archive/a.md`.
- `**/*.md` matches any markdown concept path.
- `**` matches every normalized vault path.

## Enforcement Model

Introduce a service-level `AccessPolicy` and route all REST/MCP access
objects through it. The policy exposes checks for read and write path
authorization.

Enforced operations:

- Read-like: `Read`, `Search`, `Grep`, `List`, `History`.
- Mutating: `Write`, `Edit`, `Delete`.

Non-path-specific operations:

- `Summary`, `Types`, `Validate`, and `Howto` remain available because
  they do not expose document bodies directly in the current API.
- `Log` can leak changed paths and commit subjects. With an explicit
  restricted access object, it is denied unless `read_globs` is
  effectively unrestricted (`**`) and there is no deny glob. With no
  access object, current behavior remains unchanged.
- `Push` remains REST/CLI-only and is not added to MCP.

Read-like search/list operations must not merely reject when a result is
outside the policy; they should query/filter within the authorized
scope. If multiple globs are present, the operation may either apply
query constraints where possible or post-filter results before response
budgeting. The response must not include unauthorized paths.

Mutating operations check the target path before reading, writing,
indexing, or committing.

## REST Mapping

POST bodies gain an optional `access` field for:

- `/search`
- `/grep`
- `/concepts`
- `PATCH /concepts/{path}` and `DELETE /concepts/{path}` bodies

GET routes read access params from repeated query params for
concept reads, list, history, and log. `POST /validate` and
`POST /push` are unchanged and ignore request access because this
iteration does not implement filtered validation or push ACLs.

For `POST /concepts`, `PATCH /concepts/{path}`, and
`DELETE /concepts/{path}`, `access.write_globs` governs the target
path.

For `GET /concepts/{path}` and `GET /history/{path}`,
`access.read_globs` governs the target path.

REST errors:

- Invalid glob syntax: `400 Bad Request`.
- Access denied: `403 Forbidden`.

## MCP Mapping

MCP tool inputs add the same optional nested `access` object.

Examples:

```json
{
  "path": "drafts/a.md",
  "content": "...",
  "access": {"write_globs": ["drafts/**"]}
}
```

MCP uses the same service policy and returns the same access-denied
errors through tool errors. There is no MCP-specific bypass.

## Testing

Add focused tests for:

- Glob matcher semantics: `*`, `?`, `**`, deny precedence, invalid
  patterns, missing policy vs empty policy.
- Service read/write/edit/delete authorization.
- Service search/list/grep/history filtering or rejection.
- REST request parsing for JSON `access` and repeated query glob
  params, including `403` for denied paths and `400` for invalid
  patterns.
- MCP tool inputs enforcing the same policy for read and write tools.
- `kvt_log` behavior with unrestricted, restricted, and missing access.

Run the standard verification gate:

```bash
go test ./...
go vet ./...
go build ./cmd/kvt
tmp=$(mktemp -d)
./kvt init --vault "$tmp" --defaults
rm -f ./kvt
git diff --check
```

## Documentation

Update README and AGENTS.md to mention request-scoped glob access once
implemented. Do not describe it as authenticated server-side ACLs until
API-key/session policy intersection exists.
