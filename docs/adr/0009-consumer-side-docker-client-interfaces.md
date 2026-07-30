# Docker engine seams are consumer-side interfaces, never a client mirror

Unit-testable paths in `internal/docker` reach the Docker engine through
small unexported interfaces declared next to their consumer, each listing
only the engine methods that path calls: `logsClient` for the log streamer,
`statsClient` for the stats scraper, and their successors for namespace
restore and the sidecar boot dispatch. Production passes the concrete
`*client.Client`, which satisfies every such interface structurally, so the
seam costs zero adapter code; each test file supplies its own fake, and every
method on every interface is justified by a test that fakes it. The
`Namespace.client` field stays the concrete type. Retyping it to a
package-private interface was rejected: the field's static type must carry
all of the roughly two dozen SDK methods called through it, producing a
mirror of the Docker SDK in which most methods would never gain a second
adapter - a hypothetical seam that only grows. Workflow precondition ordering
is deliberately tested against a nil client, whose panic proves no engine
call precedes the checks; do not replace those tests with fakes. Where an
inner step is unfakeable at the client seam at reasonable cost (docker exec
over a hijacked connection), seam that step itself with an unexported hook
defaulting to the real implementation instead of widening the client
interface.
