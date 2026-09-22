package parsemail

import (
	"encoding/base64"
	"io"
	"strings"
	"testing"
	"unicode/utf8"
)

// mime builds a fixture with CRLF line endings, because that is what real mail
// uses and an LF-only fixture hides trailing-CR bugs.
func mimeLines(lines ...string) string {
	return strings.Join(lines, "\r\n") + "\r\n"
}

// alternativeEmail wraps one text/plain and one text/html part, each with the
// given transfer encoding and payload, in a multipart/alternative.
func alternativeEmail(charset, cte, plain, html string) string {
	return mimeLines(
		"From: Jack <jack@example.com>",
		"To: submissions@example.com",
		"Subject: FW: Prospect",
		"MIME-Version: 1.0",
		`Content-Type: multipart/alternative; boundary="INNER"`,
		"",
		"--INNER",
		`Content-Type: text/plain; charset="`+charset+`"`,
		"Content-Transfer-Encoding: "+cte,
		"",
		plain,
		"",
		"--INNER",
		`Content-Type: text/html; charset="`+charset+`"`,
		"Content-Transfer-Encoding: "+cte,
		"",
		html,
		"",
		"--INNER--",
	)
}

// mixedEmail is the production shape: a body plus a CRQ attachment. It is the
// one case where a body-handling regression costs an attachment too.
func mixedEmail(cte, plain string) string {
	return mimeLines(
		"From: Jack <jack@example.com>",
		"To: submissions@example.com",
		"Subject: FW: Prospect",
		"MIME-Version: 1.0",
		`Content-Type: multipart/mixed; boundary="B"`,
		"",
		"--B",
		`Content-Type: text/plain; charset="utf-8"`,
		"Content-Transfer-Encoding: "+cte,
		"",
		plain,
		"",
		"--B",
		`Content-Type: text/csv; name="crq.csv"`,
		`Content-Disposition: attachment; filename="crq.csv"`,
		"Content-Transfer-Encoding: base64",
		"",
		"Y29sMSxjb2wyCjEsMg==",
		"",
		"--B--",
	)
}

func TestParse_WhenTextPartsAreBase64_DecodesBothBodies(t *testing.T) {
	email, err := Parse(strings.NewReader(alternativeEmail(
		"utf-8", "base64",
		"SW5zdXJhYmxlIHNhbGVzIOKCrDYwbSBmb3IgTW9udGHDsWEu",
		"PHA+SW5zdXJhYmxlIHNhbGVzIOKCrDYwbTwvcD4=",
	)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if want := "Insurable sales €60m for Montaña."; email.TextBody != want {
		t.Errorf("TextBody = %q, want %q", email.TextBody, want)
	}
	if want := "<p>Insurable sales €60m</p>"; email.HTMLBody != want {
		t.Errorf("HTMLBody = %q, want %q", email.HTMLBody, want)
	}
}

// Quoted-printable works only because Go's multipart reader decodes it and
// drops the header before decodeContent ever sees it. That accident carries
// almost all ASCII production mail, so it needs pinning explicitly: this also
// proves the body is decoded exactly once, not twice.
func TestParse_WhenTextPartsAreQuotedPrintable_DecodesExactlyOnce(t *testing.T) {
	email, err := Parse(strings.NewReader(alternativeEmail(
		"utf-8", "quoted-printable",
		"Cost =E2=82=AC60m for Monta=C3=B1a, wrapped here=",
		"<p>Cost =E2=82=AC60m</p>",
	)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if want := "Cost €60m for Montaña, wrapped here"; email.TextBody != want {
		t.Errorf("TextBody = %q, want %q", email.TextBody, want)
	}
	if want := "<p>Cost €60m</p>"; email.HTMLBody != want {
		t.Errorf("HTMLBody = %q, want %q", email.HTMLBody, want)
	}
}

func TestParse_WhenBase64TextIsInMultipartMixed_DecodesBodyAndKeepsAttachment(t *testing.T) {
	email, err := Parse(strings.NewReader(mixedEmail("base64", "SGVsbG8gd29ybGQ=")))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if want := "Hello world"; email.TextBody != want {
		t.Errorf("TextBody = %q, want %q", email.TextBody, want)
	}
	assertSingleCrqAttachment(t, email)
}

// A body is best-effort: senders mislabel and truncate encodings, and failing
// the parse would lose the whole email and its attachment, permanently, since
// callers treat a parse error as unretryable.
func TestParse_WhenBodyEncodingIsUnusable_DegradesAndKeepsAttachment(t *testing.T) {
	cases := map[string]struct{ cte, payload, wantBody string }{
		"base64 with an inner space": {"base64", "SGVsbG8g d29ybGQ=", "SGVsbG8g d29ybGQ="},
		"base64 missing its padding": {"base64", "SGVsbG8gd29ybGQ", "SGVsbG8gd29ybGQ"},
		"truncated base64":           {"base64", "SGVsbG8gd29ybG", "SGVsbG8gd29ybG"},
		"plain text mislabelled":     {"base64", "Hello world", "Hello world"},
		"unknown encoding":           {"x-uuencode", "Hello world", "Hello world"},
		"hyphenated 8-bit":           {"8-bit", "Hello world", "Hello world"},
		"charset in the cte header":  {"utf-8", "Hello world", "Hello world"},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			email, err := Parse(strings.NewReader(mixedEmail(c.cte, c.payload)))
			if err != nil {
				t.Fatalf("parse should degrade, not fail: %v", err)
			}

			if email.TextBody != c.wantBody {
				t.Errorf("TextBody = %q, want %q", email.TextBody, c.wantBody)
			}
			assertSingleCrqAttachment(t, email)
		})
	}
}

// Attachments stay strict: handing back corrupt bytes is worse than an error.
func TestParse_WhenAttachmentEncodingIsUnknown_Fails(t *testing.T) {
	_, err := Parse(strings.NewReader(mimeLines(
		"From: Jack <jack@example.com>",
		"To: submissions@example.com",
		"Subject: FW: Prospect",
		"MIME-Version: 1.0",
		`Content-Type: multipart/mixed; boundary="B"`,
		"",
		"--B",
		`Content-Type: text/plain; charset="utf-8"`,
		"",
		"Hello world",
		"",
		"--B",
		`Content-Type: text/csv; name="crq.csv"`,
		`Content-Disposition: attachment; filename="crq.csv"`,
		"Content-Transfer-Encoding: x-uuencode",
		"",
		"whatever",
		"",
		"--B--",
	)))

	if err == nil {
		t.Fatal("an attachment with an unknown encoding should fail the parse")
	}
}

func TestParse_WhenTransferEncodingIsSpeltOddly_StillDecodes(t *testing.T) {
	for _, cte := range []string{"BASE64", "Base64", " base64 "} {
		t.Run(cte, func(t *testing.T) {
			email, err := Parse(strings.NewReader(mixedEmail(cte, "SGVsbG8gd29ybGQ=")))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if want := "Hello world"; email.TextBody != want {
				t.Errorf("TextBody = %q, want %q", email.TextBody, want)
			}
		})
	}

	for _, cte := range []string{"7BIT", "8bit", "binary"} {
		t.Run(cte, func(t *testing.T) {
			email, err := Parse(strings.NewReader(mixedEmail(cte, "Hello world")))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if want := "Hello world"; email.TextBody != want {
				t.Errorf("TextBody = %q, want %q", email.TextBody, want)
			}
		})
	}
}

// Base64 is the encoding a mailer picks *because* the body is not ASCII, so a
// non-UTF-8 charset lands here far more often than anywhere else. Left
// unconverted it is silent corruption: json coerces the invalid bytes to U+FFFD
// rather than erroring, so a debtor name reaches the reader as "Monta?a".
func TestParse_WhenBodyCharsetIsNotUtf8_ConvertsToUtf8(t *testing.T) {
	// "Montaña costs €60m" in windows-1252: 0xF1 for n-tilde, 0x80 for euro.
	windows1252 := []byte("Monta\xf1a costs \x8060m")
	email, err := Parse(strings.NewReader(alternativeEmail(
		"windows-1252", "base64",
		base64Of(windows1252), base64Of(windows1252),
	)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if want := "Montaña costs €60m"; email.TextBody != want {
		t.Errorf("TextBody = %q, want %q", email.TextBody, want)
	}
	if !utf8.ValidString(email.TextBody) {
		t.Error("TextBody is not valid UTF-8")
	}
}

// An unknown charset must degrade rather than fail, and must still never emit
// invalid UTF-8 downstream.
func TestParse_WhenBodyCharsetIsUnknown_DegradesToValidUtf8(t *testing.T) {
	email, err := Parse(strings.NewReader(alternativeEmail(
		"x-made-up-charset", "base64",
		base64Of([]byte("Monta\xf1a")), base64Of([]byte("Monta\xf1a")),
	)))
	if err != nil {
		t.Fatalf("parse should degrade, not fail: %v", err)
	}

	if !utf8.ValidString(email.TextBody) {
		t.Errorf("TextBody is not valid UTF-8: %q", email.TextBody)
	}
}

// decodeContent's quoted-printable branch is unreachable for multipart parts
// (Go decodes and strips those), so its only live caller is the non-multipart
// body path.
func TestParse_WhenNonMultipartBodyIsQuotedPrintable_Decodes(t *testing.T) {
	email, err := Parse(strings.NewReader(mimeLines(
		"From: Jack <jack@example.com>",
		"To: submissions@example.com",
		"Subject: FW: Prospect",
		"MIME-Version: 1.0",
		"Content-Type: application/octet-stream",
		"Content-Transfer-Encoding: quoted-printable",
		"",
		"Cost =E2=82=AC60m",
	)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	content, err := io.ReadAll(email.Content)
	if err != nil {
		t.Fatalf("read content: %v", err)
	}
	// Content is the raw body rather than a trimmed text part, so unlike
	// TextBody it keeps its trailing line break.
	if want := "Cost €60m\r\n"; string(content) != want {
		t.Errorf("Content = %q, want %q", string(content), want)
	}
}

func assertSingleCrqAttachment(t *testing.T, email Email) {
	t.Helper()

	if len(email.Attachments) != 1 {
		t.Fatalf("attachments = %d, want 1", len(email.Attachments))
	}

	data, err := io.ReadAll(email.Attachments[0].Data)
	if err != nil {
		t.Fatalf("read attachment: %v", err)
	}
	if want := "col1,col2\n1,2"; string(data) != want {
		t.Errorf("attachment = %q, want %q", string(data), want)
	}
}

func base64Of(b []byte) string {
	return base64.StdEncoding.EncodeToString(b)
}

// A non-UTF-8 charset in an encoded-word header used to fail the entire parse,
// losing the email and its attachments permanently - the same failure the body
// path degrades away from, arriving through the header door.
func TestParse_WhenHeaderCharsetIsNotUtf8_DecodesAndKeepsAttachment(t *testing.T) {
	email, err := Parse(strings.NewReader(mimeLines(
		"From: =?windows-1254?Q?Mehmet_Y=FDld=FDr=FDm?= <mehmet@broker.com.tr>",
		"To: submissions@example.com",
		"Subject: =?windows-1254?Q?Limit_talebi_-_Y=FDld=FDr=FDm?=",
		"MIME-Version: 1.0",
		`Content-Type: multipart/mixed; boundary="B"`,
		"",
		"--B",
		`Content-Type: text/plain; charset="utf-8"`,
		"",
		"Hello world",
		"",
		"--B",
		`Content-Type: text/csv; name="crq.csv"`,
		`Content-Disposition: attachment; filename="crq.csv"`,
		"Content-Transfer-Encoding: base64",
		"",
		"Y29sMSxjb2wyCjEsMg==",
		"",
		"--B--",
	)))
	if err != nil {
		t.Fatalf("a non-UTF-8 header charset must not fail the parse: %v", err)
	}

	if want := "Mehmet Yıldırım"; email.From[0].Name != want {
		t.Errorf("From.Name = %q, want %q", email.From[0].Name, want)
	}
	if want := "Limit talebi - Yıldırım"; email.Subject != want {
		t.Errorf("Subject = %q, want %q", email.Subject, want)
	}
	assertSingleCrqAttachment(t, email)
}

// A single-part message declares its encoding on the message header rather than
// on a part. It was skipping transfer-decoding and charset conversion entirely,
// so a base64 body came back as its base64 text - the original defect, still
// live on this path.
func TestParse_WhenSinglePartBodyIsEncoded_DecodesIt(t *testing.T) {
	cases := map[string]struct{ charset, cte, payload, want string }{
		"base64 utf-8": {
			"utf-8", "base64", "SW5zdXJhYmxlIHNhbGVzIOKCrDYwbQ==", "Insurable sales €60m",
		},
		"base64 windows-1252": {
			"windows-1252", "base64", base64Of([]byte("Monta\xf1a costs \x8060m")), "Montaña costs €60m",
		},
		"quoted-printable": {
			"utf-8", "quoted-printable", "Cost =E2=82=AC60m", "Cost €60m",
		},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			email, err := Parse(strings.NewReader(mimeLines(
				"From: jack@example.com",
				"To: submissions@example.com",
				"Subject: FW: Prospect",
				"MIME-Version: 1.0",
				`Content-Type: text/plain; charset="`+c.charset+`"`,
				"Content-Transfer-Encoding: "+c.cte,
				"",
				c.payload,
			)))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}

			if email.TextBody != c.want {
				t.Errorf("TextBody = %q, want %q", email.TextBody, c.want)
			}
		})
	}
}

// The charset set is as wide as the senders are, so it cannot be enumerated by
// hand. These are the labels a hand-written map would predictably have missed.
func TestParse_WhenCharsetIsBeyondTheCommonSet_StillConverts(t *testing.T) {
	cases := map[string]struct {
		charset string
		body    []byte
		want    string
	}{
		"turkish windows-1254":  {"windows-1254", []byte("Y\xfdld\xfdr\xfdm \xde"), "Yıldırım Ş"},
		"simplified chinese":    {"gb2312", []byte("\xd6\xd0\xce\xc4"), "中文"},
		"cyrillic koi8-r":       {"koi8-r", []byte("\xd0\xc1\xd2\xcf"), "паро"},
		"cp1252 alias":          {"cp1252", []byte("\x8060m"), "€60m"},
		"utf8 without a hyphen": {"utf8", []byte("Montaña"), "Montaña"},
		"iso8859-1 no hyphen":   {"iso8859-1", []byte("Monta\xf1a"), "Montaña"},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			email, err := Parse(strings.NewReader(alternativeEmail(
				c.charset, "base64", base64Of(c.body), base64Of(c.body),
			)))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}

			if email.TextBody != c.want {
				t.Errorf("TextBody = %q, want %q", email.TextBody, c.want)
			}
			if !utf8.ValidString(email.TextBody) {
				t.Error("TextBody is not valid UTF-8")
			}
		})
	}
}
