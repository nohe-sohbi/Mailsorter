## 2025-05-18 - Bcrypt password byte length cap for DoS prevention
**Vulnerability:** Registration and login endpoints lacked upper bounds on password length, allowing oversized password payloads to consume excessive CPU during bcrypt hashing (CPU DoS) and causing silent password truncation past 72 bytes.
**Learning:** `golang.org/x/crypto/bcrypt` silently truncates input at 72 bytes. Input longer than 72 bytes should be rejected early in request validation.
**Prevention:** Always validate `len([]byte(password)) <= 72` before passing passwords to `bcrypt.GenerateFromPassword` or `bcrypt.CompareHashAndPassword`.
