# Templates

`omnimail.Template` renders a subject, a plain-text body and an HTML body
from one data value.

## Safe Defaults

- The HTML body uses `html/template`: data is escaped for its context, and
  unsafe URLs (for example `javascript:`) are replaced with `#ZgotmplZ`.
- The subject and text body use `text/template`.
- A missing map key is an error (`missingkey=error`) instead of `<no value>`.
- The rendered subject is collapsed to one line (control characters and
  whitespace runs become single spaces), so template data cannot inject
  headers.

## From Strings

```go
tmpl, err := omnimail.ParseTemplate("verify", omnimail.TemplateSource{
    Subject: "Verify your {{.Product}} email",
    Text:    "Hi {{.Name}},\n\nOpen this link to verify: {{.Link}}\n",
    HTML:    `<p>Hi {{.Name}},</p><p><a href="{{.Link}}">Verify your email</a></p>`,
})
```

## From Files (embed.FS)

`ParseTemplateFS(fsys, name)` reads `name.subject.tmpl`, `name.text.tmpl`
and `name.html.tmpl`. Missing files are skipped, but a text or HTML body is
required.

```
mail/
├── verify.subject.tmpl
├── verify.text.tmpl
└── verify.html.tmpl
```

```go
//go:embed mail/*.tmpl
var mailFS embed.FS

tmpl, err := omnimail.ParseTemplateFS(mailFS, "mail/verify")
```

## Rendering

```go
data := map[string]any{"Product": "Acme", "Name": user.Name, "Link": link}

// Render returns the three parts.
r, err := tmpl.Render(data) // r.Subject, r.Text, r.HTML

// Apply sets Subject (when the template has one), Text and HTML on a message.
msg := &omnimail.Message{From: from, To: []omnimail.Address{to}}
if err := tmpl.Apply(msg, data); err != nil {
    return err
}
```

## Options

```go
omnimail.ParseTemplate("x", src,
    omnimail.WithFuncs(map[string]any{"upper": strings.ToUpper}),
    omnimail.WithDelims("[[", "]]"),
)
```

Parse templates once at startup and reuse them; a `Template` is safe for
concurrent `Render` calls.
