---
implements: "@spechub/scheduling@0.1.0"
stack: [go, sqlite, htmx]
---

# scheduling-rebuild

Proof build of the SpecHub seed `@spechub/scheduling` (and the parts of `@spechub/auth` it needs),
written from the spec alone in a stack unlike the source's. Scope: [specs/rebuild.md](specs/rebuild.md).
Spec gaps met on the way: [FINDINGS.md](FINDINGS.md).

## Run

```bash
go run ./cmd/scheduling -addr :8080 -db scheduling.db
```

Open http://localhost:8080/signup. Emails are not sent; they are logged to stdout.

## Check

```bash
go vet ./... && go test ./...
```

Every in-scope requirement has a test named after its ID (`TestSLT_9` for SLT-9);
`TestSpecCoverage` fails if one is missing.

Specs in `specs/upstream/` are copied from `@spechub/scheduling` and `@spechub/auth`
(CC-BY-4.0, SpecHub, derived from Cal.com, MIT).
