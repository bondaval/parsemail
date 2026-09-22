package parsemail

import (
	"strings"
	"testing"
)

// base64TextEmail mirrors what Outlook produces when a body carries non-ASCII
// (currency symbols, accented names): the text parts inside multipart/related
// > multipart/alternative are base64, while the same email in plain ASCII would
// be quoted-printable.
const base64TextEmail = `From: Jack <jack@example.com>
To: submissions@example.com
Subject: FW: Prospect
Content-Type: multipart/related; boundary="OUTER"
MIME-Version: 1.0

--OUTER
Content-Type: multipart/alternative; boundary="INNER"

--INNER
Content-Type: text/plain; charset="utf-8"
Content-Transfer-Encoding: base64

SW5zdXJhYmxlIHNhbGVzIOKCrDYwbSBmb3IgTW9udGHDsWEu

--INNER
Content-Type: text/html; charset="utf-8"
Content-Transfer-Encoding: base64

PHA+SW5zdXJhYmxlIHNhbGVzIOKCrDYwbTwvcD4=

--INNER--

--OUTER--
`

func TestParse_Base64TextParts_AreDecoded(t *testing.T) {
	email, err := Parse(strings.NewReader(base64TextEmail))
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

// eightBitTextEmail guards the identity encodings: adding the decode step must
// not reject a part that declares one.
const eightBitTextEmail = `From: Jack <jack@example.com>
To: submissions@example.com
Subject: Plain
Content-Type: multipart/alternative; boundary="INNER"
MIME-Version: 1.0

--INNER
Content-Type: text/plain; charset="utf-8"
Content-Transfer-Encoding: 8bit

Insurable sales.

--INNER--
`

func TestParse_EightBitTextPart_ReadsThrough(t *testing.T) {
	email, err := Parse(strings.NewReader(eightBitTextEmail))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if want := "Insurable sales."; email.TextBody != want {
		t.Errorf("TextBody = %q, want %q", email.TextBody, want)
	}
}
