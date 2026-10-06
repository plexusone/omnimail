// Package omnimail is a vendor-free core for sending transactional email
// (verification links, notices, receipts) as a service.
//
// The package defines the message model ([Message], [Address]), the [Sender]
// interface every delivery provider implements, message validation with
// header-injection protection, RFC 5322 MIME assembly ([BuildMIME]) for
// SMTP-like providers, text/HTML template rendering ([Template]) and a
// classified error type ([Error]) so callers can decide whether to retry.
//
// Built-in senders live in subpackages:
//
//   - smtp: delivery to any SMTP server (STARTTLS or implicit TLS, PLAIN/LOGIN)
//   - logsender: writes messages to a slog.Logger for development
//   - memsender: captures messages in memory for tests
//
// Vendor adapters (for example AWS SES) live in separate modules and verify
// themselves with the providertest conformance suite.
//
// A minimal send:
//
//	msg := &omnimail.Message{
//	    From:    omnimail.Address{Name: "Example", Email: "no-reply@example.com"},
//	    To:      []omnimail.Address{{Email: "user@example.org"}},
//	    Subject: "Verify your email",
//	    Text:    "Open this link to verify: https://example.com/verify?t=...",
//	}
//	res, err := sender.Send(ctx, msg)
//	if err != nil {
//	    if omnimail.IsRetryable(err) {
//	        // queue for retry
//	    }
//	    return err
//	}
//	log.Println("sent", res.MessageID)
package omnimail
