# ADR-2026-10-26-sandboxed-preview-origin: Serve sandboxed-session previews from a per-task origin

**Status:** proposed for design review
**Date:** 2026-10-26
**Area:** backend, frontend, security
**Amends:** ADR-2026-09-05-trusted-browser-html-preview, for sandboxed sessions only

## Context

ADR-2026-09-05 treats previewed HTML as trusted workspace code and serves it through the same-origin session port proxy. For an unsandboxed session that is acceptable. A sandboxed session runs an agent the operator chose to confine, so its page output is untrusted: from the UI origin it could read UI cookies and storage and call the control plane as the operator.

## Decision

For a sandboxed attempt, the HTML preview and the session port proxy are served from `<task-token>.<preview.baseDomain>`. The token is random, per task, and never reused. When no base domain is configured, or `features.previewSeparateOrigin` is off, the preview is disabled with `preview_origin_unavailable`; it is never served from the UI origin. Unsandboxed sessions keep the ADR-2026-09-05 behavior. ADR-2026-09-15 (preview feedback capture) keeps its boundary and must treat the preview origin as the page origin for sandboxed sessions.

## Consequences

Desktop and loopback installs have no wildcard names, so sandboxed previews are unavailable there until a base domain exists. Deployments need wildcard DNS and TLS for the base domain. UI cookies must be host-only.

## Alternatives considered

Keeping the same origin and relying on the iframe sandbox attribute was rejected: the proxy cannot force it on top-level navigation. A single shared preview origin for all tasks was rejected because tasks could read each other's storage.
