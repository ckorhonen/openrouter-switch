# CLI and Auth Review Fixes Design

## Goal

Resolve four review findings without changing request routing.

## Design

- Give `/v1/admin/model-catalog` its own 25-second client timeout because the
  endpoint may synchronously hydrate an account catalog for up to 20 seconds.
  Keep the 500-millisecond mutation timeout for status polling and mutation
  confirmation.
- Treat router auth status `forbidden` as a failed doctor check. Show sanitized
  server detail and direct the user to `openrouter-switch auth set-key`.
- Validate `global.auth` deterministically. Allow only `anthropic`. Reject
  `openrouter` with Keychain and `auth set-key` guidance; reject every other key
  as unsupported.
- Make gateway credential resolution delegate to
  `auth.ResolveDefaultAPIKey` whenever no test or embedder resolver is
  configured. This preserves Keychain-before-environment precedence and fails
  closed when the canonical resolver fails.

## Testing

- Delay a model-catalog response beyond the short mutation timeout and confirm
  eligibility still succeeds.
- Add doctor coverage for `forbidden`.
- Add routing-policy tables for allowed, OpenRouter, and unknown auth keys.
- Verify gateway fallback calls the canonical resolver and returns its
  Keychain-sourced result even when environment/config values exist.

