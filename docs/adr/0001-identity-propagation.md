# ADR-0001: Every service verifies the JWT itself

**Status:** Accepted
**Date:** 2026-09-24
**Deciders:** Maintainers

## Context

Before this decision, the gateway checked that a JWT was valid and then
proxied the request with no identity attached. No backend service knew who
was calling, so nothing behind the gateway could enforce roles or ownership
(audit P0-5). The backends were also published on host ports, so the gateway
check could be skipped entirely (P0-6).

Identity has to reach every service in a form the service can trust. Two
ways to do that were on the table.

## Decision

**Every service verifies the caller's JWT itself**, using shared middleware
in `pkg/auth`. The gateway still verifies too, so bad tokens are rejected at
the edge, and then forwards the original `Authorization` header unchanged.
Handlers read the principal (`auth.FromContext`) and enforce roles
(`auth.RequireRole`) and ownership (`claims.UserID` / `claims.RefID`).

## Options considered

### Option A: the gateway forwards a signed principal

The gateway strips any client-sent `X-User-*` headers, then adds
`X-User-Id`, `X-User-Role`, `X-Ref-Id` and an HMAC over them. Services
verify the HMAC.

| Dimension | Assessment |
|---|---|
| Complexity | Medium: a second signing scheme to design, version and test |
| Blast radius if a service is reached directly | Safe only if the HMAC check is right everywhere |
| Service-to-service calls | Each caller has to re-sign |
| Familiarity | Custom, with no library support |

**Pros:** services never see the raw token, and header parsing is cheap.
**Cons:** it invents a second token format. Getting "strip client headers"
wrong is a classic spoofing bug. It also duplicates what a JWT already is.

### Option B: every service verifies the JWT (chosen)

| Dimension | Assessment |
|---|---|
| Complexity | Low: `pkg/auth.Middleware` already exists, so each router adds one line |
| Blast radius if a service is reached directly | Still authenticated: defence in depth for P0-6 |
| Service-to-service calls | Forward the caller's `Authorization` header, so authz runs as the end user |
| Familiarity | Standard, with a maintained library (`golang-jwt/jwt/v5`) |

**Pros:** one mechanism, nothing new to invent, and every service is safe on
its own. The gateway WebSocket can authorize "may this user watch this
order?" by calling the Order service *as the user* and relying on that
service's ownership check.
**Cons:** with HS256, every service holds the signing secret, so any
compromised service could mint tokens.

## Trade-off analysis

The deciding factor is failure mode. With Option A, one mistake in header
stripping or HMAC checking silently turns every request into whoever the
attacker claims to be. With Option B, the worst misconfiguration (a missing
middleware line) is caught by the route-role matrix tests in
`docs/TESTING.md`. The shared-secret weakness in B is real, but it's cheap
to close later without touching handlers: switch to RS256 or EdDSA, so the
gateway alone signs and services hold only the public key.

## Consequences

- **Easier:** authz lives next to the handler that needs it. Direct access to
  a service, for example from a debug port, is no longer a bypass.
  Service-to-service calls carry the user's rights.
- **Harder:** every service needs `JWT_SECRET`. Outside `PROFILE=dev` it must
  be present and strong, or the service refuses to boot.
- **Revisit:** asymmetric signing (RS256/EdDSA + JWKS), so only the gateway
  can mint tokens. Short token lifetimes plus refresh tokens. Service
  identities for background jobs, if one ever needs to call another service
  outside a user request.

## Action items

1. [x] `pkg/auth`: `Middleware`, `RequireRole`, and principal helpers with
   ownership checks, covered by a table-driven test.
2. [x] Every service router: public routes (`/healthz`, `/metrics`,
   registration, login verify) outside the middleware, everything else inside.
3. [x] The gateway forwards `Authorization` unchanged. The WebSocket takes
   `?token=` (browsers can't set WebSocket headers) and authorizes by calling
   `GET /orders/{id}` as the user.
4. [x] `pkg/config`: fail fast on a missing or weak `JWT_SECRET` outside `PROFILE=dev`.
5. [ ] Later: asymmetric signing (tracked in docs/AUDIT.md).
