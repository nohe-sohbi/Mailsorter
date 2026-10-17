## 2025-05-18 - Bcrypt password byte length cap for DoS prevention
**Vulnerability:** Registration and login endpoints lacked upper bounds on password length, allowing oversized password payloads to consume excessive CPU during bcrypt hashing (CPU DoS) and causing silent password truncation past 72 bytes.
**Learning:** `golang.org/x/crypto/bcrypt` silently truncates input at 72 bytes. Input longer than 72 bytes should be rejected early in request validation.
**Prevention:** Always validate `len([]byte(password)) <= 72` before passing passwords to `bcrypt.GenerateFromPassword` or `bcrypt.CompareHashAndPassword`.

## 2025-05-19 - Centralized egress validation for user-provided base URLs
**Vulnerability:** User-supplied AI base URLs in BYOK settings were only checked for basic HTTP/HTTPS scheme structure, allowing SSRF attacks targeting loopback interfaces, local networks, or cloud metadata endpoints (169.254.169.254).
**Learning:** Checking `net.ParseIP` on URL hostnames misses hostname aliases like `localhost` or `.local`. Egress validation must check both hostname patterns and parsed IP addresses.
**Prevention:** Always validate external/custom URLs using `internal/egress.Parse` rather than custom `url.Parse` checks.

## 2026-10-16 - Batch endpoint array slice bounds for DoS prevention
**Vulnerability:** Batch AI endpoints (`AnalyzeEmails` and `ApplyBatch`) lacked upper bound checks on input slice lengths, allowing arbitrary-length arrays in JSON payloads to exhaust server CPU, memory, database operations, and external AI service calls (DoS).
**Learning:** Batch handlers decoding array fields into structs must validate slice size bounds right after JSON decoding, before reaching external service logic or database queries.
**Prevention:** Always check `len(req.SliceField) > maxBatchSize` immediately after `decodeJSON` in batch API handlers.

## 2025-05-19 - Transport guard bypass in multi-transport API handlers
**Vulnerability:** Handlers (`Unsubscribe`, `ApplyBulk`, `CreateSmartLabel`) called `getUserToken` directly instead of `gmailClientFor`, bypassing transport verification for non-Gmail/IMAP accounts.
**Learning:** Calling token resolution helpers directly instead of transport-aware wrappers (`gmailClientFor` or `openSession`) bypasses transport checks, risking execution of Gmail mutations with IMAP message/folder identifiers.
**Prevention:** Always route transport-specific client instantiation through `gmailClientFor` (for Gmail-only paths) or `openSession` (for multi-transport paths) rather than calling `getUserToken` directly.
