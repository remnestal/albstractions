# URI SANs on leaf certificates, issued verbatim or not at all

`WithURIs(uris ...string)` sets the URI SANs of an issued leaf, preserving
order. It follows the existing `Option` idiom rather than extending the
`Generate*Cert` signatures: URI SANs are optional, and mandatory arguments are
the only thing that belongs in a signature. Like `WithSerial`, the option stores
its raw input and validation is deferred to an unexported `resolve*` helper, so
a bad value surfaces as an error from the `Generate*Cert` call rather than
panicking at option-construction time.

The motivating consumer is a SPIFFE ID such as
`spiffe://example.org/service/api`, read back as `cert.URIs[0]` — the identity
`mesh`'s `node.Peer.ID()` prefers over any DNS name or the CommonName. Ordering
matters because that consumer reads index 0 specifically.

## Considered options

- **`WithURIs(uris ...*url.URL)`**: type-safe and error-free at option
  construction, but pushes a `url.Parse` and its error handling onto every
  caller that holds a SPIFFE ID as a string, which is all of them. Rejected as
  boilerplate that buys no safety the string form doesn't already provide —
  see the validation policy below.
- **Adding a `uris []string` parameter to `GenerateServerCert` and friends**:
  breaks every existing caller for a feature most do not use.

## A URI SAN satisfies the "at least one SAN" requirement

`generateCert` refuses to issue a server or peer certificate with no SAN. That
guard predates URI SANs and counted only DNS names and IP addresses, so a
URI-only leaf — a peer identified purely by SPIFFE ID, with no hostname — was
unmintable. `len(uris)` now counts toward it.

This is safe for existing callers: they pass no URIs, so the guard rejects
exactly what it rejected before. The alternative, requiring a DNS name or IP
alongside every SPIFFE ID, would force callers to invent a filler hostname that
nothing verifies against.

## URIs are rejected, never rewritten

`url.Parse` alone is not sufficient validation. It is permissive enough to
accept `""` and `example.org/service/api` without error, and — the real hazard —
it is free to *normalise* its input. Since `x509.CreateCertificate` serialises
`u.String()`, a normalising parse would mint a certificate bearing a different
identity than the caller asked for, silently. For an identity string that
downstream authorization allowlists on, that is the worst possible failure mode.

`resolveURIs` therefore rejects, rather than repairs, three classes of input:

- **Not absolute** (no scheme): `""`, `/service/api`,
  `example.org/service/api`.
- **Non-ASCII**: URI SANs are encoded as `IA5String`, and a non-ASCII byte
  produces a certificate other implementations reject or read back differently.
- **Anything that fails `u.String() == raw`**: an unescaped space, an uppercase
  scheme that `url.Parse` would lowercase, a redundantly encoded octet. The
  caller gets an error naming the form that *would* have been issued.

The round-trip check is the strict one, and deliberately so — it is what makes
"the SPIFFE ID in the certificate is byte-for-byte the one you passed" a
property of the API rather than a hope.

- **Normalise and accept** (issue `u.String()` whatever it is): rejected. Quiet
  identity rewriting is not a tradeoff worth making for the convenience of
  accepting `SPIFFE://…`.
- **Parse with no validation beyond the error return**: rejected as barely more
  than no validation at all, for the reasons above.

## Scope

`WithURIs` is shared with `GenerateCA` through `certConfig` but is ignored
there, documented the same way `WithMaxPathLen` documents its inverse. Trust
domains, SPIFFE-specific validation, and SPIRE integration are all out of scope:
`certkit` issues a URI SAN, it does not interpret one.
