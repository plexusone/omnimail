package omnimail

import (
	"bytes"
	"errors"
	"fmt"
	htmltemplate "html/template"
	"io/fs"
	"strings"
	texttemplate "text/template"
)

// TemplateSource holds the template text for one email: a subject line, a
// plain-text body and an HTML body. Text or HTML (or both) is required;
// Subject is optional.
type TemplateSource struct {
	Subject string
	Text    string
	HTML    string
}

// TemplateOption configures [ParseTemplate] and [ParseTemplateFS].
type TemplateOption func(*templateConfig)

type templateConfig struct {
	funcs map[string]any
	delim [2]string
}

// WithFuncs adds template functions available to the subject, text and HTML
// templates.
func WithFuncs(funcs map[string]any) TemplateOption {
	return func(c *templateConfig) {
		for k, v := range funcs {
			c.funcs[k] = v
		}
	}
}

// WithDelims sets the action delimiters (default "{{" and "}}").
func WithDelims(left, right string) TemplateOption {
	return func(c *templateConfig) { c.delim = [2]string{left, right} }
}

// Template renders the subject and bodies of a transactional email.
//
// Safe defaults: the HTML body uses html/template, so data is contextually
// autoescaped; the subject and text body use text/template; a missing map key
// is an error rather than "<no value>"; and the rendered subject is collapsed
// to a single line so data can never inject headers.
type Template struct {
	name    string
	subject *texttemplate.Template
	text    *texttemplate.Template
	html    *htmltemplate.Template
}

// Rendered is the output of [Template.Render].
type Rendered struct {
	Subject string
	Text    string
	HTML    string
}

// ParseTemplate parses an email template from strings.
func ParseTemplate(name string, src TemplateSource, opts ...TemplateOption) (*Template, error) {
	if src.Text == "" && src.HTML == "" {
		return nil, fmt.Errorf("omnimail: template %q: a text or HTML body is required", name)
	}
	cfg := templateConfig{funcs: map[string]any{}, delim: [2]string{"{{", "}}"}}
	for _, o := range opts {
		o(&cfg)
	}
	t := &Template{name: name}
	var err error
	if src.Subject != "" {
		if t.subject, err = texttemplate.New(name+".subject").Delims(cfg.delim[0], cfg.delim[1]).
			Funcs(cfg.funcs).Option("missingkey=error").Parse(src.Subject); err != nil {
			return nil, fmt.Errorf("omnimail: template %q subject: %w", name, err)
		}
	}
	if src.Text != "" {
		if t.text, err = texttemplate.New(name+".text").Delims(cfg.delim[0], cfg.delim[1]).
			Funcs(cfg.funcs).Option("missingkey=error").Parse(src.Text); err != nil {
			return nil, fmt.Errorf("omnimail: template %q text: %w", name, err)
		}
	}
	if src.HTML != "" {
		if t.html, err = htmltemplate.New(name+".html").Delims(cfg.delim[0], cfg.delim[1]).
			Funcs(cfg.funcs).Option("missingkey=error").Parse(src.HTML); err != nil {
			return nil, fmt.Errorf("omnimail: template %q html: %w", name, err)
		}
	}
	return t, nil
}

// Template file suffixes read by [ParseTemplateFS].
const (
	SubjectSuffix = ".subject.tmpl"
	TextSuffix    = ".text.tmpl"
	HTMLSuffix    = ".html.tmpl"
)

// ParseTemplateFS parses the email template called name from fsys, reading
// name+".subject.tmpl", name+".text.tmpl" and name+".html.tmpl". Missing
// files are skipped, but at least one body file must exist. name may include
// a directory, e.g. "mail/verify". It works with embed.FS:
//
//	//go:embed mail/*.tmpl
//	var mailFS embed.FS
//	tmpl, err := omnimail.ParseTemplateFS(mailFS, "mail/verify")
func ParseTemplateFS(fsys fs.FS, name string, opts ...TemplateOption) (*Template, error) {
	var src TemplateSource
	for _, f := range []struct {
		suffix string
		dst    *string
	}{{SubjectSuffix, &src.Subject}, {TextSuffix, &src.Text}, {HTMLSuffix, &src.HTML}} {
		b, err := fs.ReadFile(fsys, name+f.suffix)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("omnimail: template %q: %w", name, err)
		}
		*f.dst = string(b)
	}
	return ParseTemplate(name, src, opts...)
}

// Name returns the template name.
func (t *Template) Name() string { return t.name }

// Render executes the templates with data.
func (t *Template) Render(data any) (*Rendered, error) {
	var r Rendered
	var buf bytes.Buffer
	if t.subject != nil {
		if err := t.subject.Execute(&buf, data); err != nil {
			return nil, fmt.Errorf("omnimail: render %q subject: %w", t.name, err)
		}
		r.Subject = singleLine(buf.String())
		buf.Reset()
	}
	if t.text != nil {
		if err := t.text.Execute(&buf, data); err != nil {
			return nil, fmt.Errorf("omnimail: render %q text: %w", t.name, err)
		}
		r.Text = buf.String()
		buf.Reset()
	}
	if t.html != nil {
		if err := t.html.Execute(&buf, data); err != nil {
			return nil, fmt.Errorf("omnimail: render %q html: %w", t.name, err)
		}
		r.HTML = buf.String()
	}
	return &r, nil
}

// Apply renders the template with data and sets msg's Subject (when the
// template has one), Text and HTML.
func (t *Template) Apply(msg *Message, data any) error {
	r, err := t.Render(data)
	if err != nil {
		return err
	}
	if t.subject != nil {
		msg.Subject = r.Subject
	}
	msg.Text = r.Text
	msg.HTML = r.HTML
	return nil
}

// singleLine collapses all whitespace runs (including line breaks and other
// control characters) into single spaces and trims the result.
func singleLine(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}
