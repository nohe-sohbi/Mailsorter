## 2025-05-18 - Bcrypt password byte length cap for DoS prevention
**Vulnerability:** Registration and login endpoints lacked upper bounds on password length, allowing oversized password payloads to consume excessive CPU during bcrypt hashing (CPU DoS) and causing silent password truncation past 72 bytes.
**Learning:** `golang.org/x/crypto/bcrypt` silently truncates input at 72 bytes. Input longer than 72 bytes should be rejected early in request validation.
**Prevention:** Always validate `len([]byte(password)) <= 72` before passing passwords to `bcrypt.GenerateFromPassword` or `bcrypt.CompareHashAndPassword`.

## 2025-05-19 - Transport guard bypass in multi-transport API handlers
**Vulnerability:** Handlers (`Unsubscribe`, `ApplyBulk`, `CreateSmartLabel`) called `getUserToken` directly instead of `gmailClientFor`, bypassing transport verification for non-Gmail/IMAP accounts.
**Learning:** Calling token resolution helpers directly instead of transport-aware wrappers (`gmailClientFor` or `openSession`) bypasses transport checks, risking execution of Gmail mutations with IMAP message/folder identifiers.
**Prevention:** Always route transport-specific client instantiation through `gmailClientFor` (for Gmail-only paths) or `openSession` (for multi-transport paths) rather than calling `getUserToken` directly.
