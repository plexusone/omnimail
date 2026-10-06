package omnimail

import (
	"errors"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

func TestParseTemplateRender(t *testing.T) {
	tmpl, err := ParseTemplate("verify", TemplateSource{
		Subject: "Verify {{.Product}}\n for {{.Name}}",
		Text:    "Hi {{.Name}}, open {{.Link}}",
		HTML:    `<p>Hi {{.Name}}</p><a href="{{.Link}}">Verify</a>`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if tmpl.Name() != "verify" {
		t.Fatalf("Name = %q", tmpl.Name())
	}
	data := map[string]string{
		"Product": "Acme",
		"Name":    "<script>alert(1)</script>\r\nBcc: evil@example.com",
		"Link":    "javascript:alert(1)",
	}
	r, err := tmpl.Render(data)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(r.Subject, "\r\n") {
		t.Fatalf("subject contains a line break: %q", r.Subject)
	}
	if r.Subject != "Verify Acme for <script>alert(1)</script> Bcc: evil@example.com" {
		t.Fatalf("subject = %q", r.Subject)
	}
	if strings.Contains(r.HTML, "<script>") {
		t.Fatalf("HTML not escaped: %q", r.HTML)
	}
	if !strings.Contains(r.HTML, "#ZgotmplZ") {
		t.Fatalf("unsafe URL not filtered: %q", r.HTML)
	}
	if !strings.Contains(r.Text, "<script>") {
		t.Fatalf("text body should be raw: %q", r.Text)
	}

	msg := validMessage()
	if err := tmpl.Apply(msg, data); err != nil {
		t.Fatal(err)
	}
	if msg.Subject != r.Subject || msg.HTML != r.HTML || msg.Text != r.Text {
		t.Fatal("Apply did not set fields")
	}
	if err := msg.Validate(); err != nil {
		t.Fatalf("rendered message invalid: %v", err)
	}
}

func TestTemplateMissingKey(t *testing.T) {
	tmpl, err := ParseTemplate("x", TemplateSource{Subject: "{{.A}}", Text: "{{.B}}", HTML: "{{.C}}"})
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range []map[string]string{{}, {"A": "a"}, {"A": "a", "B": "b"}} {
		if _, err := tmpl.Render(data); err == nil {
			t.Errorf("Render(%v) should fail on missing key", data)
		}
	}
	msg := validMessage()
	if err := tmpl.Apply(msg, map[string]string{}); err == nil {
		t.Error("Apply should fail")
	}
}

func TestTemplateWithoutSubjectKeepsSubject(t *testing.T) {
	tmpl, err := ParseTemplate("x", TemplateSource{Text: "body"})
	if err != nil {
		t.Fatal(err)
	}
	msg := validMessage()
	msg.Subject = "keep"
	if err := tmpl.Apply(msg, nil); err != nil {
		t.Fatal(err)
	}
	if msg.Subject != "keep" || msg.Text != "body" || msg.HTML != "" {
		t.Fatalf("msg = %+v", msg)
	}
}

func TestTemplateOptions(t *testing.T) {
	tmpl, err := ParseTemplate("x", TemplateSource{Subject: "[[up .]]", Text: "[[up .]]", HTML: "<b>[[up .]]</b>"},
		WithDelims("[[", "]]"), WithFuncs(map[string]any{"up": strings.ToUpper}))
	if err != nil {
		t.Fatal(err)
	}
	r, err := tmpl.Render("hi")
	if err != nil {
		t.Fatal(err)
	}
	if r.Subject != "HI" || r.Text != "HI" || r.HTML != "<b>HI</b>" {
		t.Fatalf("rendered = %+v", r)
	}
}

func TestParseTemplateErrors(t *testing.T) {
	if _, err := ParseTemplate("x", TemplateSource{Subject: "s"}); err == nil {
		t.Error("expected error without body")
	}
	for _, src := range []TemplateSource{
		{Subject: "{{", Text: "t"},
		{Text: "{{"},
		{HTML: "{{"},
	} {
		if _, err := ParseTemplate("x", src); err == nil {
			t.Errorf("ParseTemplate(%+v) should fail", src)
		}
	}
}

func TestParseTemplateFS(t *testing.T) {
	fsys := fstest.MapFS{
		"mail/verify.subject.tmpl": {Data: []byte("Verify {{.}}")},
		"mail/verify.text.tmpl":    {Data: []byte("Text {{.}}")},
		"mail/verify.html.tmpl":    {Data: []byte("<p>{{.}}</p>")},
		"mail/notice.text.tmpl":    {Data: []byte("Notice")},
	}
	tmpl, err := ParseTemplateFS(fsys, "mail/verify")
	if err != nil {
		t.Fatal(err)
	}
	r, err := tmpl.Render("you")
	if err != nil {
		t.Fatal(err)
	}
	if r.Subject != "Verify you" || r.Text != "Text you" || r.HTML != "<p>you</p>" {
		t.Fatalf("rendered = %+v", r)
	}
	notice, err := ParseTemplateFS(fsys, "mail/notice")
	if err != nil {
		t.Fatal(err)
	}
	if r, err := notice.Render(nil); err != nil || r.Text != "Notice" || r.HTML != "" {
		t.Fatalf("notice = %+v, %v", r, err)
	}
	if _, err := ParseTemplateFS(fsys, "mail/missing"); err == nil {
		t.Fatal("expected error for missing template")
	}
	if _, err := ParseTemplateFS(errFS{}, "x"); !errors.Is(err, errBoom) {
		t.Fatalf("expected read error, got %v", err)
	}
}

var errBoom = errors.New("boom")

type errFS struct{}

func (errFS) Open(string) (fs.File, error) { return nil, errBoom }
