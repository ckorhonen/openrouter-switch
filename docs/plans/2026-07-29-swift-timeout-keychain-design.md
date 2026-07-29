# Swift Gateway Timeout and Keychain Update Design

## Goal

Match Swift client timeouts to gateway endpoint budgets while retaining fast
failure for ordinary local admin calls, and keep Keychain ACLs add-only.

## Design

- Set every gateway `URLRequest` to the existing 2-second default.
- Override model catalog requests to 22 seconds for the gateway's 20-second
  server budget.
- Override credential reload requests to 7 seconds for the gateway's 5-second
  server budget.
- Keep the default session resource cap above the longest endpoint request.
- Attempt Keychain updates with `kSecValueData` only.
- Create and attach `kSecAttrAccess` only when adding a missing item.
- Use pure attribute builders so tests inspect the exact Security dictionaries
  without mutating the developer's Keychain.

## Testing

- Capture outgoing requests with `URLProtocol` and assert 2, 7, and 22 seconds.
- Assert update attributes omit `kSecAttrAccess`.
- Assert add attributes retain `kSecAttrAccess`.
- Run focused tests, then the full Swift suite.
