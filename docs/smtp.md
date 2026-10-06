# SMTP

Package `smtp` delivers through any SMTP server: a relay such as Postfix, a
provider's SMTP endpoint, or a development server such as Mailpit. It uses
only the standard library.

```go
sender, err := smtp.New(smtp.Config{
    Host:     "smtp.example.com",
    Username: os.Getenv("SMTP_USERNAME"),
    Password: os.Getenv("SMTP_PASSWORD"),
})
```

## Configuration

| Field | Default | Notes |
|-------|---------|-------|
| `Host` | required | Server host name; also the TLS `ServerName` |
| `Port` | 587 / 465 / 25 | By `Security` |
| `Security` | `StartTLS` | `StartTLS` (required upgrade), `ImplicitTLS` (SMTPS), `Plaintext` |
| `Username`, `Password` | none | Enables AUTH when `Username` is set |
| `Auth` | `AuthAuto` | `AuthPlain`, `AuthLogin`; auto prefers PLAIN |
| `AllowInsecureAuth` | false | Allow credentials over an unencrypted connection (local tests only) |
| `TLSConfig` | TLS 1.2+ | Custom roots, client certificates |
| `LocalName` | `localhost` | EHLO name |
| `DialTimeout` | 10s | Connect and TLS handshake |
| `Timeout` | 60s | Whole session; a shorter context deadline wins |
| `MessageIDDomain` | From domain | Right-hand side of generated Message-IDs |
| `Dialer` | `net.Dialer` | Custom dialing (proxies, tests) |

## Behavior

- `StartTLS` is mandatory when selected: if the server does not offer
  STARTTLS, `Send` fails rather than sending in plaintext.
- Credentials are never sent over an unencrypted connection unless
  `AllowInsecureAuth` is set.
- Each `Send` opens its own connection, so a `Sender` is safe for concurrent
  use.
- Canceling the context interrupts the session immediately; the error matches
  `context.Canceled` / `context.DeadlineExceeded` and is not retryable.
- Messages with internationalized addresses require the server's SMTPUTF8
  extension; otherwise `Send` returns `KindInvalidAddress` before MAIL FROM.
- Bcc recipients are added to the envelope only.

## Error Classification

| Reply | Kind |
|-------|------|
| 530, 534, 535, 538, 5.7.8 | `Auth` |
| 4xx with 4.7.28, 4.4.5 or rate/limit wording | `Throttled` |
| other 4xx, network errors, timeouts | `Transient` |
| 5.1.x, 5.6.7, or 550/551/553/501 at MAIL/RCPT | `InvalidAddress` |
| other 5xx | `Rejected` |
| certificate verification failures | `Unknown` (not retryable) |

The reply code is in `Error.Code`.

## Testing Against a Fake Server

`smtp/smtptest` is an in-process SMTP server:

```go
srv, err := smtptest.NewServer(smtptest.Options{})
if err != nil {
    t.Fatal(err)
}
defer srv.Close()

sender, err := smtp.New(smtp.Config{Host: srv.Host(), Port: srv.Port(), Security: smtp.Plaintext})
// ... send ...
env := srv.Messages()[0] // env.From, env.To, env.Data
```

It supports STARTTLS and implicit TLS (`smtptest.NewTLSConfig()` returns a
trusted self-signed pair), AUTH PLAIN/LOGIN, SMTPUTF8, and failure injection:

```go
srv.FailNext(smtptest.Failure{Stage: smtptest.StageRcpt, Code: 550, Message: "5.1.1 User unknown"})
srv.FailNext(smtptest.Failure{Stage: smtptest.StageData, Stall: true}) // hang until the client gives up
```
