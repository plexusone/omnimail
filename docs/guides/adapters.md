# Writing a Provider Adapter

A provider adapter implements `omnimail.Sender` for one delivery service. It
lives in its own module next to the vendor SDK (for example AWS SES in
`github.com/plexusone/omni-aws/omnimail`), so applications that do not select
that provider never download its SDK.

## The Contract

```go
type Sender interface {
    Send(ctx context.Context, msg *omnimail.Message) (*omnimail.SendResult, error)
}
```

An adapter must:

1. **Validate first.** Call `omnimail.ValidateMessage(msg)` and return its
   error unchanged (it matches `omnimail.ErrInvalidMessage`) without calling
   the provider. This also handles a nil message.
2. **Honor the context.** Return an error matching `ctx.Err()` when the
   context is canceled or its deadline passes, and do not send. Check
   `ctx.Err()` before the request and pass `ctx` to the SDK call.
3. **Not modify `msg`.** Copy before transforming (`msg.Clone()`).
4. **Keep Bcc out of headers.** Bcc recipients go only to the envelope or
   destination list.
5. **Classify failures.** Return `*omnimail.Error` with the right `Kind`,
   `Provider` set to the adapter name, `Code` set to the provider's error
   code, `RetryAfter` when the provider suggests one, and the SDK error in
   `Err`.
6. **Report the result.** `SendResult.MessageID` is the provider's message ID
   (or the RFC 5322 Message-ID when the provider returns none);
   `SendResult.Provider` is the adapter name.
7. **Be safe for concurrent use.**

## Mapping the Message

Providers fall into two groups:

- **Raw MIME** (SMTP, SES `Content.Raw`, Gmail `users.messages.send`):
  build the message with `omnimail.BuildMIME(msg, omnimail.BuildOptions{})`
  and send `MIMEMessage.Data`, with `msg.Recipients()` as the envelope /
  destinations. This gives full fidelity for headers, encodings and Unicode.
- **Structured API** (SendGrid v3, SES `Content.Simple`): map fields
  directly. Map `Headers` to the provider's custom header field, and skip
  any the provider forbids by returning `KindInvalidMessage` rather than
  silently dropping them.

`Tags` map to provider metadata (SES message tags, SendGrid categories or
custom args). If the provider restricts tag characters, either document and
sanitize consistently or return `KindInvalidMessage` naming the tag.
`IdempotencyKey` maps to the provider's idempotency mechanism when one
exists; otherwise it is ignored.

## Error Mapping

Choose kinds by what the caller should do next:

| Kind | Caller action | Typical provider signals |
|------|---------------|--------------------------|
| `InvalidMessage` | fix the request | malformed request, forbidden header |
| `InvalidAddress` | fix or drop the address | invalid or suppressed recipient, unverified recipient in sandbox |
| `Rejected` | stop; investigate | content or policy rejection, sending paused, sender not verified |
| `Throttled` | back off, retry | HTTP 429, throttling or quota exceeded |
| `Transient` | retry with backoff | HTTP 5xx, timeouts, connection resets |
| `Auth` | fix credentials or permissions | HTTP 401/403, invalid or expired credentials |
| `Unknown` | log; treat as not retryable | anything else |

Many SDKs retry internally. Keep the SDK's retries modest (or off) so the
application's retry policy, driven by `IsRetryable`, stays in control.

## Example: AWS SES v2

A sketch of the SES adapter shape (`omni-aws/omnimail`):

```go
type Sender struct {
    client           *sesv2.Client
    configurationSet string
}

func (s *Sender) Send(ctx context.Context, msg *omnimail.Message) (*omnimail.SendResult, error) {
    if err := omnimail.ValidateMessage(msg); err != nil {
        return nil, err
    }
    if err := ctx.Err(); err != nil {
        return nil, err
    }
    raw, err := omnimail.BuildMIME(msg, omnimail.BuildOptions{})
    if err != nil {
        return nil, err
    }
    in := &sesv2.SendEmailInput{
        FromEmailAddress: aws.String(msg.From.Email),
        Destination:      &types.Destination{ToAddresses: emails(msg.Recipients())},
        Content:          &types.EmailContent{Raw: &types.RawMessage{Data: raw.Data}},
        EmailTags:        tags(msg.Tags),
    }
    if s.configurationSet != "" {
        in.ConfigurationSetName = aws.String(s.configurationSet)
    }
    out, err := s.client.SendEmail(ctx, in)
    if err != nil {
        if ctxErr := ctx.Err(); ctxErr != nil {
            return nil, ctxErr
        }
        return nil, classify(err) // *omnimail.Error with Provider "ses"
    }
    return &omnimail.SendResult{MessageID: aws.ToString(out.MessageId), Provider: "ses"}, nil
}
```

Suggested SES error mapping: `TooManyRequestsException` and
`LimitExceededException` to `Throttled`; `MessageRejected`,
`MailFromDomainNotVerifiedException`, `AccountSuspendedException` and
`SendingPausedException` to `Rejected`; `BadRequestException` to
`InvalidMessage` (or `InvalidAddress` when it names an address); credential
and permission errors (`AccessDeniedException`, `UnrecognizedClientException`,
expired tokens, HTTP 403) to `Auth`; HTTP 5xx and network errors to
`Transient`.

## Testing an Adapter

Point the SDK at a fake endpoint in the test and run the
[conformance suite](conformance.md). For SES v2, an `httptest.Server`
handles `POST /v2/email/outbound-emails`, decodes the JSON body, base64
decodes `Content.Raw.Data`, and hands it to `providertest.DecodeMIME` with the
destination addresses; it copies `EmailTags` into the decoded message's
`Tags`. Failures are injected by answering with the provider's status code
and error type (for example HTTP 429 with `X-Amzn-ErrorType:
TooManyRequestsException`).

```go
func TestConformance(t *testing.T) {
    fake := newFakeSES(t) // httptest.Server + capture + failure queue
    sender := New(sesv2.New(sesv2.Options{
        BaseEndpoint: aws.String(fake.URL),
        Region:       "us-east-1",
        Credentials:  credentials.NewStaticCredentialsProvider("AKID", "SECRET", ""),
        RetryMaxAttempts: 1,
    }))
    providertest.RunAll(t, providertest.Config{
        Harness:      fake.Harness(sender),
        Provider:     "ses",
        SupportsTags: true,
    })
}
```

Keep live-provider integration tests separate and opt-in (for example behind
an environment variable), and rely on the fake endpoint for CI.
