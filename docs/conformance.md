# Conformance Suite

`providertest` verifies that a sender behaves like every other omnimail
provider. Adapters run it in their own unit tests against a fake provider
endpoint; no credentials or network access are needed.

## Harness

```go
type Harness interface {
    Sender() omnimail.Sender                     // sender wired to the fake endpoint
    Captured(t testing.TB) []*omnimail.Message   // what the endpoint accepted since Reset
    Reset()                                      // clear captures and pending failures
    InjectFailure(kind omnimail.Kind) bool       // make the next send fail like the provider would
}
```

`Captured` returns the messages as the provider received them, decoded back
into `omnimail.Message`:

- Raw-MIME providers decode with `providertest.DecodeMIME(raw, envelopeRecipients)`.
  Envelope recipients not in To/Cc become Bcc; custom headers land in
  `Headers` (a transmitted `Bcc` header shows up there, so leaks fail the
  suite).
- Structured-API providers build the message from the request fields.
- Fill `Tags` and `IdempotencyKey` from the request when the provider
  transmits them.

`InjectFailure` should produce the provider's native failure (an SMTP reply
code, an HTTP status and error code) so the adapter's real classification
code runs. Return false for kinds the fake cannot simulate; those checks are
skipped.

## Config

| Field | Meaning |
|-------|---------|
| `Harness` | Required |
| `Provider` | Expected `SendResult.Provider` and `Error.Provider` (empty skips) |
| `From` | Sender used in test messages (default `no-reply@example.com`) |
| `SupportsTags` | Assert tags reach the provider |
| `SupportsIdempotencyKey` | Assert the idempotency key reaches the provider |
| `SupportsSMTPUTF8` | Assert internationalized addresses are delivered |
| `SkipCustomHeaders` | Skip the custom header check |
| `Timeout` | Per-send timeout (default 10s) |

## Checks

| Check | Verifies |
|-------|----------|
| `SendResult` | non-nil result, non-empty `MessageID`, expected `Provider` |
| `TextOnly`, `HTMLOnly`, `TextAndHTML` | bodies arrive intact (line endings normalized) and nothing extra is added |
| `Addressing` | multiple To/Cc with display names, Bcc delivered but not in headers, Bcc-only messages |
| `ReplyTo` | Reply-To transmitted |
| `UnicodeHeaders` | non-ASCII subject, display names and body |
| `CustomHeaders` | custom headers transmitted (unless skipped) |
| `Tags`, `IdempotencyKey` | accepted always; transmitted when supported |
| `InternationalAddress` | SMTPUTF8 addresses (when supported) |
| `DoesNotMutateMessage` | `Send` leaves the caller's message unchanged |
| `InvalidMessage` | missing recipients/body, header injection, bad address, reserved header and nil are rejected with `ErrInvalidMessage` before reaching the endpoint |
| `ContextCanceled` | canceled context returns `context.Canceled`, not retryable, nothing sent |
| `ErrorClassification` | each injected failure maps to the right `Kind`, `Provider` and `IsRetryable`; failures do not persist |
| `ConcurrentSends` | parallel sends all arrive |

## Examples in This Repository

- `providertest/providertest_test.go` runs the suite against `memsender`.
- `smtp/smtp_test.go` runs it against the SMTP sender over STARTTLS and
  implicit TLS with the in-process `smtptest` server, mapping each kind to an
  SMTP reply (550 5.1.1, 554 5.7.1, 450 4.7.0, 451 4.3.0, 535 5.7.8).
