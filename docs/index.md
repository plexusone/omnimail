# OmniMail

OmniMail is a vendor-free Go core for **transactional email**: verification
links, security notices, receipts and other mail an application sends as
itself, as a service.

It provides:

- a validated message model (`Message`, `Address`) with header-injection protection
- one `Sender` interface implemented by every delivery provider
- RFC 5322 MIME assembly for SMTP-like providers (`BuildMIME`)
- text/HTML templates with safe defaults (`Template`)
- classified errors so callers know whether to retry (`Error`, `Kind`)
- built-in senders: SMTP, log (development) and memory (tests)
- a conformance suite (`providertest`) every provider adapter runs

The core uses only the Go standard library. Vendor SDKs live in adapter
modules, so applications depend on the small core and the deployment selects
the provider.

## Providers

| Provider | Module / Package | Status |
|----------|------------------|--------|
| SMTP | `github.com/plexusone/omnimail/smtp` | Available |
| Log (development) | `github.com/plexusone/omnimail/logsender` | Available |
| Memory (tests) | `github.com/plexusone/omnimail/memsender` | Available |
| AWS SES v2 | `github.com/plexusone/omni-aws/omnimail` | Planned |
| SendGrid | `github.com/plexusone/omni-twilio/omnimail` | Planned |
| Gmail API | `github.com/plexusone/omni-google/omnimail` | Planned |

## Package Layout

| Package | Purpose |
|---------|---------|
| `omnimail` | Message model, `Sender`, validation, MIME assembly, templates, errors |
| `omnimail/smtp` | SMTP sender |
| `omnimail/smtp/smtptest` | In-process SMTP server for tests |
| `omnimail/logsender` | Sender that writes to `slog` |
| `omnimail/memsender` | Sender that captures messages in memory |
| `omnimail/providertest` | Provider conformance suite and MIME decoder |

## Scope

OmniMail sends transactional mail. It is not a mailbox client, a marketing
list manager or a conversational messaging library (see
[OmniChat](https://github.com/plexusone/omnichat) for that). Deliverability
(SPF, DKIM, DMARC, bounce and complaint handling) is configured on the
provider the deployment selects.
