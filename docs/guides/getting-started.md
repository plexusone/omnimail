# Getting Started

## Install

```bash
go get github.com/plexusone/omnimail
```

## Build a Message

```go
msg := &omnimail.Message{
    From:    omnimail.Address{Name: "Example", Email: "no-reply@example.com"},
    To:      []omnimail.Address{{Name: "Jane", Email: "jane@example.org"}},
    ReplyTo: []omnimail.Address{{Email: "support@example.com"}},
    Subject: "Verify your email",
    Text:    "Open this link to verify: https://example.com/verify?t=...",
    HTML:    `<p><a href="https://example.com/verify?t=...">Verify</a></p>`,
    Headers: map[string]string{"X-Entity-Ref-ID": "verify-123"},
    Tags:    map[string]string{"purpose": "verify_email"},
}
```

Rules enforced by `msg.Validate()` (every sender calls it):

- `From` and every recipient must be a valid bare address (`local@domain`).
  Display names go in `Name`. Internationalized addresses are accepted.
- At least one of `To`, `Cc`, `Bcc`.
- At least one of `Text`, `HTML` (both produces `multipart/alternative`).
- No line breaks or control characters in `Subject`, display names, custom
  header values, tags or the idempotency key. This prevents header injection.
- `Headers` cannot set structural headers (`From`, `To`, `Subject`,
  `Content-Type`, `Message-ID`, ...); see `omnimail.ReservedHeader`.

Parse user input with `omnimail.ParseAddress("Jane <jane@example.org>")` or
`omnimail.ParseAddressList`.

## Choose a Sender

```go
// Production: an SMTP relay or provider SMTP endpoint.
sender, err := smtp.New(smtp.Config{Host: "smtp.example.com", Username: u, Password: p})

// Local development: log instead of delivering (bodies redacted by default).
sender := logsender.New(slog.Default())

// Tests: capture messages in memory.
sender := memsender.New()
```

Application code should accept an `omnimail.Sender` so the deployment can
choose.

## Send and Handle Errors

```go
res, err := sender.Send(ctx, msg)
if err != nil {
    switch omnimail.KindOf(err) {
    case omnimail.KindInvalidMessage, omnimail.KindInvalidAddress:
        // caller bug or bad user input; do not retry
    case omnimail.KindThrottled:
        // back off; omnimail.RetryAfter(err) may suggest a delay
    case omnimail.KindTransient:
        // retry with backoff
    case omnimail.KindAuth:
        // credentials or permissions; alert operators
    case omnimail.KindRejected:
        // provider refused the message (policy, suppression list, sender not verified)
    }
    return err
}
log.Printf("sent %s via %s", res.MessageID, res.Provider)
```

`omnimail.IsRetryable(err)` is true for throttled and transient failures,
except when the failure was caused by the caller's context being canceled or
timing out. Sentinels work with `errors.Is`: `ErrInvalidMessage`,
`ErrInvalidAddress`, `ErrRejected`, `ErrThrottled`, `ErrTransient`, `ErrAuth`.

## Test Code That Sends Mail

```go
func TestSignUpSendsVerification(t *testing.T) {
    mail := memsender.New()
    app := NewApp(mail)
    if err := app.SignUp(ctx, "jane@example.org"); err != nil {
        t.Fatal(err)
    }
    if mail.Len() != 1 || !strings.Contains(mail.Last().Text, "/verify?") {
        t.Fatalf("verification mail = %+v", mail.Last())
    }
}
```

`memsender` can also fail on demand: `mail.FailNext(omnimail.NewError(omnimail.KindThrottled, "memory", nil))`.
