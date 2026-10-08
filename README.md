# OmniMail

[![Go CI][go-ci-svg]][go-ci-url]
[![Go Lint][go-lint-svg]][go-lint-url]
[![Go SAST][go-sast-svg]][go-sast-url]
[![Coverage][coverage-svg]][coverage-url]
[![Docs][docs-godoc-svg]][docs-godoc-url]
[![Docs][docs-mkdoc-svg]][docs-mkdoc-url]
[![License][license-svg]][license-url]

 [go-ci-svg]: https://github.com/plexusone/omnimail/actions/workflows/go-ci.yaml/badge.svg?branch=main
 [go-ci-url]: https://github.com/plexusone/omnimail/actions/workflows/go-ci.yaml
 [go-lint-svg]: https://github.com/plexusone/omnimail/actions/workflows/go-lint.yaml/badge.svg?branch=main
 [go-lint-url]: https://github.com/plexusone/omnimail/actions/workflows/go-lint.yaml
 [go-sast-svg]: https://github.com/plexusone/omnimail/actions/workflows/go-sast-codeql.yaml/badge.svg?branch=main
 [go-sast-url]: https://github.com/plexusone/omnimail/actions/workflows/go-sast-codeql.yaml
 [coverage-svg]: https://img.shields.io/badge/coverage-92.9%25-brightgreen
 [coverage-url]: https://github.com/plexusone/omnimail
 [docs-godoc-svg]: https://pkg.go.dev/badge/github.com/plexusone/omnimail
 [docs-godoc-url]: https://pkg.go.dev/github.com/plexusone/omnimail
 [docs-mkdoc-svg]: https://img.shields.io/badge/Go-dev%20guide-blue.svg
 [docs-mkdoc-url]: https://plexusone.dev/omnimail
 [license-svg]: https://img.shields.io/badge/license-MIT-blue.svg
 [license-url]: https://github.com/plexusone/omnimail/blob/main/LICENSE

A vendor-free Go core for **transactional email**: verification links,
security notices, receipts and other mail an application sends as itself.

OmniMail defines one `Sender` interface, a validated message model, MIME
assembly, templates and classified errors. Applications depend only on the
core; the deployment chooses the delivery provider. Vendor SDKs live in
separate adapter modules, so the core has **no dependencies outside the Go
standard library**.

> OmniMail is not a mailbox or chat client. For conversational messaging
> authenticated as a user, see [OmniChat](https://github.com/plexusone/omnichat).

## Features

- **One interface** - `Sender.Send(ctx, *Message) (*SendResult, error)` for every provider
- **Safe by default** - address validation via `net/mail`, internationalized addresses, header-injection protection (CR/LF rejected in every header value)
- **MIME assembly** - RFC 5322 `multipart/alternative`, quoted-printable/base64 bodies, RFC 2047 headers, `Date` and `Message-ID`; Bcc never written
- **Templates** - subject/text/HTML rendering with `html/template` autoescaping, from strings or an `fs.FS` (`embed.FS`)
- **Classified errors** - `InvalidMessage`, `InvalidAddress`, `Rejected`, `Throttled`, `Transient`, `Auth` with `Retryable()`
- **Built-in senders** - SMTP (STARTTLS / implicit TLS, PLAIN/LOGIN), log (bodies redacted), memory (tests)
- **Conformance suite** - `providertest` keeps every adapter behaving the same

## Providers

| Provider | Module / Package | Status |
|----------|------------------|--------|
| SMTP | `github.com/plexusone/omnimail/smtp` | Available |
| Log (development) | `github.com/plexusone/omnimail/logsender` | Available |
| Memory (tests) | `github.com/plexusone/omnimail/memsender` | Available |
| AWS SES v2 | `github.com/plexusone/omni-aws/omnimail` | Planned |
| SendGrid | `github.com/plexusone/omni-twilio/omnimail` | Planned |
| Gmail API | `github.com/plexusone/omni-google/omnimail` | Planned |

## Installation

```bash
go get github.com/plexusone/omnimail
```

## Quick Start

```go
package main

import (
    "context"
    "log"
    "os"

    "github.com/plexusone/omnimail"
    "github.com/plexusone/omnimail/smtp"
)

func main() {
    sender, err := smtp.New(smtp.Config{
        Host:     os.Getenv("SMTP_HOST"),
        Username: os.Getenv("SMTP_USERNAME"),
        Password: os.Getenv("SMTP_PASSWORD"),
    })
    if err != nil {
        log.Fatal(err)
    }

    msg := &omnimail.Message{
        From:    omnimail.Address{Name: "Example", Email: "no-reply@example.com"},
        To:      []omnimail.Address{{Email: "user@example.org"}},
        Subject: "Verify your email",
        Text:    "Open this link to verify your address: https://example.com/verify?t=...",
        HTML:    `<p><a href="https://example.com/verify?t=...">Verify your address</a></p>`,
        Tags:    map[string]string{"purpose": "verify_email"},
    }

    res, err := sender.Send(context.Background(), msg)
    if err != nil {
        if omnimail.IsRetryable(err) {
            log.Printf("temporary failure, retry later: %v", err)
            return
        }
        log.Fatal(err)
    }
    log.Printf("sent %s via %s", res.MessageID, res.Provider)
}
```

## Templates

```go
//go:embed mail/*.tmpl
var mailFS embed.FS

tmpl, err := omnimail.ParseTemplateFS(mailFS, "mail/verify") // verify.subject.tmpl, verify.text.tmpl, verify.html.tmpl
if err != nil {
    return err
}
if err := tmpl.Apply(msg, map[string]any{"Name": user.Name, "Link": link}); err != nil {
    return err
}
```

## Errors

```go
_, err := sender.Send(ctx, msg)
switch {
case errors.Is(err, omnimail.ErrInvalidMessage): // fix the message; never retry
case errors.Is(err, omnimail.ErrThrottled):      // back off: omnimail.RetryAfter(err)
case omnimail.IsRetryable(err):                  // transient: retry with backoff
case errors.Is(err, omnimail.ErrAuth):           // credentials or permissions
}
```

## Testing

```go
s := memsender.New()
app := NewApp(s)
// ... exercise the app ...
if s.Len() != 1 || !strings.Contains(s.Last().Text, "/verify?") {
    t.Fatal("verification email not sent")
}
```

## Writing a Provider Adapter

Adapters implement `omnimail.Sender` in their own module and run the
conformance suite against a fake endpoint:

```go
func TestConformance(t *testing.T) {
    providertest.RunAll(t, providertest.Config{
        Harness:      newFakeEndpointHarness(t),
        Provider:     "ses",
        SupportsTags: true,
    })
}
```

See the [adapter guide](docs/guides/adapters.md) and [conformance suite](docs/guides/conformance.md).

## Documentation

- [Getting Started](docs/guides/getting-started.md)
- [Templates](docs/guides/templates.md)
- [SMTP](docs/guides/smtp.md)
- [Writing a Provider Adapter](docs/guides/adapters.md)
- [Conformance Suite](docs/guides/conformance.md)

## License

MIT License - see [LICENSE](LICENSE) for details.
