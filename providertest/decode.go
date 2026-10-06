package providertest

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"strings"

	"github.com/plexusone/omnimail"
)

// standardHeaders are decoded into Message fields or are transport
// metadata; every other header is returned in Message.Headers.
var standardHeaders = map[string]bool{
	"From": true, "To": true, "Cc": true, "Reply-To": true, "Subject": true,
	"Date": true, "Message-Id": true, "Mime-Version": true,
	"Content-Type": true, "Content-Transfer-Encoding": true,
	"Received": true, "Return-Path": true,
}

// DecodeMIME decodes a raw RFC 5322 message, as received by a fake SMTP
// server or a raw-message API endpoint, into an omnimail.Message for
// comparison by the suite. envelopeRecipients are the SMTP RCPT TO (or API
// destination) addresses; those not in To or Cc become Bcc. Tags and
// IdempotencyKey are not part of MIME and are left empty for the caller to
// fill from the provider request.
func DecodeMIME(raw []byte, envelopeRecipients []string) (*omnimail.Message, error) {
	m, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("providertest: parse message: %w", err)
	}
	dec := new(mime.WordDecoder)
	out := &omnimail.Message{}

	from, err := addressList(m.Header, "From")
	if err != nil {
		return nil, err
	}
	if len(from) > 0 {
		out.From = from[0]
	}
	if out.To, err = addressList(m.Header, "To"); err != nil {
		return nil, err
	}
	if out.Cc, err = addressList(m.Header, "Cc"); err != nil {
		return nil, err
	}
	if out.ReplyTo, err = addressList(m.Header, "Reply-To"); err != nil {
		return nil, err
	}
	if out.Subject, err = dec.DecodeHeader(m.Header.Get("Subject")); err != nil {
		return nil, fmt.Errorf("providertest: decode subject: %w", err)
	}
	for k, vs := range m.Header {
		key := textproto.CanonicalMIMEHeaderKey(k)
		if standardHeaders[key] {
			continue
		}
		v, err := dec.DecodeHeader(strings.Join(vs, ", "))
		if err != nil {
			return nil, fmt.Errorf("providertest: decode header %s: %w", key, err)
		}
		if out.Headers == nil {
			out.Headers = map[string]string{}
		}
		out.Headers[key] = v
	}

	visible := map[string]bool{}
	for _, a := range append(append([]omnimail.Address{}, out.To...), out.Cc...) {
		visible[strings.ToLower(a.Email)] = true
	}
	for _, r := range envelopeRecipients {
		if !visible[strings.ToLower(r)] {
			out.Bcc = append(out.Bcc, omnimail.Address{Email: r})
		}
	}

	if err := decodeBody(textproto.MIMEHeader(m.Header), m.Body, out); err != nil {
		return nil, err
	}
	return out, nil
}

func addressList(h mail.Header, key string) ([]omnimail.Address, error) {
	if h.Get(key) == "" {
		return nil, nil
	}
	list, err := h.AddressList(key)
	if err != nil {
		return nil, fmt.Errorf("providertest: parse %s: %w", key, err)
	}
	out := make([]omnimail.Address, len(list))
	for i, a := range list {
		out[i] = omnimail.Address{Name: a.Name, Email: a.Address}
	}
	return out, nil
}

func decodeBody(h textproto.MIMEHeader, body io.Reader, out *omnimail.Message) error {
	ctype := h.Get("Content-Type")
	if ctype == "" {
		ctype = "text/plain"
	}
	mt, params, err := mime.ParseMediaType(ctype)
	if err != nil {
		return fmt.Errorf("providertest: parse content type: %w", err)
	}
	if strings.HasPrefix(mt, "multipart/") {
		mr := multipart.NewReader(body, params["boundary"])
		for {
			p, err := mr.NextRawPart()
			if errors.Is(err, io.EOF) {
				return nil
			}
			if err != nil {
				return fmt.Errorf("providertest: read part: %w", err)
			}
			if err := decodeBody(p.Header, p, out); err != nil {
				return err
			}
		}
	}
	var r io.Reader = body
	switch strings.ToLower(h.Get("Content-Transfer-Encoding")) {
	case "quoted-printable":
		r = quotedprintable.NewReader(body)
	case "base64":
		r = base64.NewDecoder(base64.StdEncoding, body) // the decoder skips CR and LF
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("providertest: decode %s body: %w", mt, err)
	}
	switch mt {
	case "text/plain":
		out.Text = string(b)
	case "text/html":
		out.HTML = string(b)
	}
	return nil
}
