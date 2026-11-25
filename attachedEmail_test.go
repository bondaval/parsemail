package parsemail

import (
	"io"
	"strings"
	"testing"
)

// Outlook's "attach item" shape: the attached email has a disposition but no
// filename and no name.
const attachedEmailInner = `From: broker@example.com
To: underwriter@example.com
Subject: =?Windows-1252?Q?You=92ve_been_sent_large_files?=
Date: Wed, 25 Nov 2026 08:00:31 +0000
Content-Type: text/plain; charset="utf-8"

Download link: https://files.example.com/abc
`

var outlookAttachedEmail = `From: Underwriter <underwriter@example.com>
To: submissions@inbound.example.com
Subject: FW: New request
Date: Wed, 25 Nov 2026 12:15:42 +0000
Message-ID: <outer@example.com>
MIME-Version: 1.0
Content-Type: multipart/mixed; boundary="outer"

--outer
Content-Type: multipart/alternative; boundary="alt"

--alt
Content-Type: text/plain; charset="utf-8"

See the attached email.
--alt
Content-Type: text/html; charset="utf-8"

<p>See the attached email.</p>
--alt--

--outer
Content-Type: application/pdf; name="application.pdf"
Content-Disposition: attachment; filename="application.pdf"
Content-Transfer-Encoding: base64

JVBERi0xLjQK
--outer
Content-Type: message/rfc822
Content-Disposition: attachment;
	creation-date="Wed, 25 Nov 2026 08:00:50 GMT";
	modification-date="Wed, 25 Nov 2026 12:15:42 GMT"

` + attachedEmailInner + `
--outer--
`

var attachedEmailWithoutSubject = `From: Underwriter <underwriter@example.com>
Subject: FW: New request
Content-Type: multipart/mixed; boundary="outer"

--outer
Content-Type: text/plain; charset="utf-8"

Body
--outer
Content-Type: message/rfc822

From: broker@example.com
Content-Type: text/plain

No subject here
--outer--
`

var attachmentDispositionWithoutAnyName = `From: Underwriter <underwriter@example.com>
Subject: FW: New request
Content-Type: multipart/mixed; boundary="outer"

--outer
Content-Type: text/plain; charset="utf-8"

Body
--outer
Content-Type: application/octet-stream
Content-Disposition: attachment
Content-Transfer-Encoding: base64

AQID
--outer--
`

var unnamedInlinePart = `From: Underwriter <underwriter@example.com>
Subject: FW: New request
Content-Type: multipart/mixed; boundary="outer"

--outer
Content-Type: text/plain; charset="utf-8"

Body
--outer
Content-Type: application/octet-stream

AQID
--outer--
`

func TestParse_OutlookAttachedEmail_KeptAsNamedAttachment(t *testing.T) {
	e, err := Parse(strings.NewReader(outlookAttachedEmail))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	if e.TextBody != "See the attached email." {
		t.Errorf("text body = %q", e.TextBody)
	}
	got := readAttachments(t, e)
	if len(got) != 2 {
		t.Fatalf("want 2 attachments, got %d: %v", len(got), got)
	}
	if got[0].filename != "application.pdf" || got[0].contentType != "application/pdf" || got[0].data != "%PDF-1.4\n" {
		t.Errorf("pdf attachment = %+v", got[0])
	}
	attached := got[1]
	if attached.filename != "You’ve been sent large files.eml" {
		t.Errorf("attached email filename = %q", attached.filename)
	}
	if attached.contentType != "message/rfc822" {
		t.Errorf("attached email content type = %q", attached.contentType)
	}
	if attached.data != attachedEmailInner {
		t.Errorf("attached email bytes changed:\n%q", attached.data)
	}

	reparsed, err := Parse(strings.NewReader(attached.data))
	if err != nil {
		t.Fatalf("attached email does not parse on its own: %v", err)
	}
	if reparsed.TextBody != "Download link: https://files.example.com/abc" {
		t.Errorf("attached email body = %q", reparsed.TextBody)
	}
}

func TestParse_AttachedEmailWithoutSubject_GetsDefaultName(t *testing.T) {
	e, err := Parse(strings.NewReader(attachedEmailWithoutSubject))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	got := readAttachments(t, e)
	if len(got) != 1 || got[0].filename != "attached-email.eml" || got[0].contentType != "message/rfc822" {
		t.Errorf("attachments = %+v", got)
	}
}

func TestParse_AttachmentDispositionWithoutAnyName_GetsDefaultName(t *testing.T) {
	e, err := Parse(strings.NewReader(attachmentDispositionWithoutAnyName))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	got := readAttachments(t, e)
	if len(got) != 1 || got[0].filename != "attachment" || got[0].data != "\x01\x02\x03" {
		t.Errorf("attachments = %+v", got)
	}
}

// An unnamed part that is neither marked as an attachment nor an email is
// still rejected, as before.
func TestParse_UnnamedPartNotMarkedAsAttachment_StillFails(t *testing.T) {
	_, err := Parse(strings.NewReader(unnamedInlinePart))
	if err == nil || !strings.Contains(err.Error(), "Unknown multipart/mixed nested mime type: application/octet-stream") {
		t.Errorf("err = %v", err)
	}
}

func readAttachments(t *testing.T, e Email) []attachmentData {
	t.Helper()
	var out []attachmentData
	for _, a := range e.Attachments {
		b, err := io.ReadAll(a.Data)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, attachmentData{filename: a.Filename, contentType: a.ContentType, data: string(b)})
	}
	return out
}

var twoAttachedEmailsWithTheSameSubject = `From: Underwriter <underwriter@example.com>
Subject: FW: Quote
Content-Type: multipart/mixed; boundary="outer"

--outer
Content-Type: text/plain; charset="utf-8"

Body
--outer
Content-Type: application/pdf; name="Re: Quote.eml"
Content-Disposition: attachment; filename="Re: Quote.eml"

not an email, but it already holds the name
--outer
Content-Type: message/rfc822
Content-Disposition: attachment

From: broker@example.com
Subject: Re: Quote
Content-Type: text/plain

First reply
--outer
Content-Type: message/rfc822
Content-Disposition: attachment

From: broker@example.com
Subject: Re: Quote
Content-Type: text/plain

Second reply
--outer
Content-Type: application/octet-stream
Content-Disposition: attachment

AQID
--outer
Content-Type: application/octet-stream
Content-Disposition: attachment

BAUG
--outer--
`

func TestParse_UnnamedAttachmentsWithTheSameGeneratedName_AreNumbered(t *testing.T) {
	e, err := Parse(strings.NewReader(twoAttachedEmailsWithTheSameSubject))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	var names []string
	for _, at := range readAttachments(t, e) {
		names = append(names, at.filename)
	}
	want := []string{"Re: Quote.eml", "Re: Quote (2).eml", "Re: Quote (3).eml", "attachment", "attachment (2)"}
	if strings.Join(names, "|") != strings.Join(want, "|") {
		t.Errorf("names = %q, want %q", names, want)
	}
}

var twoAttachmentsTheSenderNamedAlike = `From: Underwriter <underwriter@example.com>
Subject: Logos
Content-Type: multipart/mixed; boundary="outer"

--outer
Content-Type: text/plain; charset="utf-8"

Body
--outer
Content-Type: image/png; name="image001.png"
Content-Disposition: attachment; filename="image001.png"
Content-Transfer-Encoding: base64

AQID
--outer
Content-Type: image/png; name="image001.png"
Content-Disposition: attachment; filename="image001.png"
Content-Transfer-Encoding: base64

BAUG
--outer--
`

func TestParse_DuplicateNamesTheSenderGave_AreLeftAlone(t *testing.T) {
	e, err := Parse(strings.NewReader(twoAttachmentsTheSenderNamedAlike))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	got := readAttachments(t, e)
	if len(got) != 2 || got[0].filename != "image001.png" || got[1].filename != "image001.png" {
		t.Errorf("attachments = %+v", got)
	}
}
