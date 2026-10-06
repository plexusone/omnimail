package omnimail

import (
	"errors"
	"testing"
)

func TestParseAddress(t *testing.T) {
	tests := []struct {
		in      string
		want    Address
		wantErr bool
	}{
		{in: "jane@example.com", want: Address{Email: "jane@example.com"}},
		{in: "Jane Doe <jane@example.com>", want: Address{Name: "Jane Doe", Email: "jane@example.com"}},
		{in: "=?utf-8?q?Jos=C3=A9?= <jose@example.com>", want: Address{Name: "José", Email: "jose@example.com"}},
		{in: "用户@例子.广告", want: Address{Email: "用户@例子.广告"}},
		{in: "not-an-address", wantErr: true},
		{in: "", wantErr: true},
		{in: "jane@example.com\r\nBcc: evil@example.com", wantErr: true},
	}
	for _, tt := range tests {
		got, err := ParseAddress(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("ParseAddress(%q) = %v, want error", tt.in, got)
				continue
			}
			if KindOf(err) != KindInvalidAddress || !errors.Is(err, ErrInvalidAddress) || !errors.Is(err, ErrInvalidMessage) {
				t.Errorf("ParseAddress(%q) error %v not classified as invalid address", tt.in, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseAddress(%q): %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ParseAddress(%q) = %+v, want %+v", tt.in, got, tt.want)
		}
	}
}

func TestMustParseAddress(t *testing.T) {
	if got := MustParseAddress("a@example.com"); got.Email != "a@example.com" {
		t.Fatalf("got %+v", got)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	MustParseAddress("bogus")
}

func TestParseAddressList(t *testing.T) {
	got, err := ParseAddressList("a@example.com, B <b@example.com>")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].Name != "B" || got[1].Email != "b@example.com" {
		t.Fatalf("got %+v", got)
	}
	for _, bad := range []string{"a@example.com, nope", "a@example.com\nb@example.com"} {
		if _, err := ParseAddressList(bad); !errors.Is(err, ErrInvalidAddress) {
			t.Errorf("ParseAddressList(%q) err = %v", bad, err)
		}
	}
}

func TestAddressValidate(t *testing.T) {
	valid := []Address{
		{Email: "a@example.com"},
		{Name: "Zoë", Email: "zoe@exämple.com"},
		{Email: "user@localhost"},
	}
	for _, a := range valid {
		if err := a.Validate(); err != nil {
			t.Errorf("Validate(%+v): %v", a, err)
		}
	}
	invalid := []Address{
		{},
		{Email: "Bob <bob@example.com>"},
		{Email: "<bob@example.com>"},
		{Email: "bob@example.com\r\n"},
		{Name: "Bob\r\nBcc: x@example.com", Email: "bob@example.com"},
		{Name: "Bob\x00", Email: "bob@example.com"},
		{Email: "bob"},
		{Email: "\xff@example.com"},
	}
	for _, a := range invalid {
		if err := a.Validate(); !errors.Is(err, ErrInvalidAddress) {
			t.Errorf("Validate(%+v) = %v, want invalid address", a, err)
		}
	}
}

func TestAddressHelpers(t *testing.T) {
	a := Address{Name: "Zoë Ünicode", Email: "zoe@example.com"}
	if a.Domain() != "example.com" {
		t.Errorf("Domain = %q", a.Domain())
	}
	if (Address{Email: "nope"}).Domain() != "" {
		t.Error("Domain of address without @ should be empty")
	}
	if !a.IsASCII() || (Address{Email: "用户@例子.广告"}).IsASCII() {
		t.Error("IsASCII mismatch")
	}
	if got := a.String(); got != "=?utf-8?q?Zo=C3=AB_=C3=9Cnicode?= <zoe@example.com>" {
		t.Errorf("String = %q", got)
	}
	if got := (Address{Email: "x@example.com"}).String(); got != "x@example.com" {
		t.Errorf("String = %q", got)
	}
	if got := (Address{Name: "Doe, Jane", Email: "j@example.com"}).String(); got != `"Doe, Jane" <j@example.com>` {
		t.Errorf("String = %q", got)
	}
}
