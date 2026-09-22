package parsemail

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"strings"
	"time"
)

const contentTypeMultipartMixed = "multipart/mixed"
const contentTypeMultipartAlternative = "multipart/alternative"
const contentTypeMultipartRelated = "multipart/related"
const contentTypeTextHtml = "text/html"
const contentTypeTextPlain = "text/plain"

// Parse an email message read from io.Reader into parsemail.Email struct
func Parse(r io.Reader) (email Email, err error) {
	msg, err := mail.ReadMessage(r)
	if err != nil {
		return
	}

	email, err = createEmailFromHeader(msg.Header)
	if err != nil {
		return
	}

	email.ContentType = msg.Header.Get("Content-Type")
	contentType, params, err := parseContentType(email.ContentType)
	if err != nil {
		return
	}

	switch contentType {
	case contentTypeMultipartMixed:
		email.TextBody, email.HTMLBody, email.Attachments, email.EmbeddedFiles, err = parseMultipartMixed(msg.Body, params["boundary"])
	case contentTypeMultipartAlternative:
		email.TextBody, email.HTMLBody, email.EmbeddedFiles, err = parseMultipartAlternative(msg.Body, params["boundary"])
	case contentTypeMultipartRelated:
		email.TextBody, email.HTMLBody, email.EmbeddedFiles, err = parseMultipartRelated(msg.Body, params["boundary"])
	// A single-part message carries its transfer encoding and charset on the
	// message header rather than on a part, so it needs the same treatment a
	// part gets - without it a base64 single-part body comes back as its base64
	// text, which is the whole bug this file exists to avoid.
	case contentTypeTextPlain:
		var body string
		body, err = readTextBody(msg.Body, textproto.MIMEHeader(msg.Header))
		email.TextBody = trimTrailingNewline(body)
	case contentTypeTextHtml:
		var body string
		body, err = readTextBody(msg.Body, textproto.MIMEHeader(msg.Header))
		email.HTMLBody = trimTrailingNewline(body)
	default:
		email.Content, err = decodeContent(msg.Body, msg.Header.Get("Content-Transfer-Encoding"))
	}

	return
}

func createEmailFromHeader(header mail.Header) (email Email, err error) {
	hp := headerParser{header: &header}

	email.Subject = decodeMimeSentence(header.Get("Subject"))
	email.From = hp.parseAddressList(header.Get("From"))
	email.Sender = hp.parseAddress(header.Get("Sender"))
	email.ReplyTo = hp.parseAddressList(header.Get("Reply-To"))
	email.To = hp.parseAddressList(header.Get("To"))
	email.Cc = hp.parseAddressList(header.Get("Cc"))
	email.Bcc = hp.parseAddressList(header.Get("Bcc"))
	email.Date = hp.parseTime(header.Get("Date"))
	email.ResentFrom = hp.parseAddressList(header.Get("Resent-From"))
	email.ResentSender = hp.parseAddress(header.Get("Resent-Sender"))
	email.ResentTo = hp.parseAddressList(header.Get("Resent-To"))
	email.ResentCc = hp.parseAddressList(header.Get("Resent-Cc"))
	email.ResentBcc = hp.parseAddressList(header.Get("Resent-Bcc"))
	email.ResentMessageID = hp.parseMessageId(header.Get("Resent-Message-ID"))
	email.MessageID = hp.parseMessageId(header.Get("Message-ID"))
	email.InReplyTo = hp.parseMessageIdList(header.Get("In-Reply-To"))
	email.References = hp.parseMessageIdList(header.Get("References"))
	email.ResentDate = hp.parseTime(header.Get("Resent-Date"))

	if hp.err != nil {
		err = hp.err
		return
	}

	//decode whole header for easier access to extra fields
	//todo: should we decode? aren't only standard fields mime encoded?
	email.Header = decodeHeaderMime(header)

	return
}

func parseContentType(contentTypeHeader string) (contentType string, params map[string]string, err error) {
	if contentTypeHeader == "" {
		contentType = contentTypeTextPlain
		return
	}

	return mime.ParseMediaType(contentTypeHeader)
}

func parseMultipartRelated(msg io.Reader, boundary string) (textBody, htmlBody string, embeddedFiles []EmbeddedFile, err error) {
	pmr := multipart.NewReader(msg, boundary)
	for {
		part, err := pmr.NextPart()

		if err == io.EOF {
			break
		} else if err != nil {
			return textBody, htmlBody, embeddedFiles, err
		}

		contentType, params, err := mime.ParseMediaType(part.Header.Get("Content-Type"))
		if err != nil {
			return textBody, htmlBody, embeddedFiles, err
		}

		switch contentType {
		case contentTypeTextPlain:
			ppContent, err := readTextPart(part)
			if err != nil {
				return textBody, htmlBody, embeddedFiles, err
			}

			textBody += trimTrailingNewline(ppContent)
		case contentTypeTextHtml:
			ppContent, err := readTextPart(part)
			if err != nil {
				return textBody, htmlBody, embeddedFiles, err
			}

			htmlBody += trimTrailingNewline(ppContent)
		case contentTypeMultipartAlternative:
			tb, hb, ef, err := parseMultipartAlternative(part, params["boundary"])
			if err != nil {
				return textBody, htmlBody, embeddedFiles, err
			}

			htmlBody += hb
			textBody += tb
			embeddedFiles = append(embeddedFiles, ef...)
		default:
			if isEmbeddedFile(part) {
				ef, err := decodeEmbeddedFile(part)
				if err != nil {
					return textBody, htmlBody, embeddedFiles, err
				}

				embeddedFiles = append(embeddedFiles, ef)
			} else {
				return textBody, htmlBody, embeddedFiles, fmt.Errorf("Can't process multipart/related inner mime type: %s", contentType)
			}
		}
	}

	return textBody, htmlBody, embeddedFiles, err
}

func parseMultipartAlternative(msg io.Reader, boundary string) (textBody, htmlBody string, embeddedFiles []EmbeddedFile, err error) {
	pmr := multipart.NewReader(msg, boundary)
	for {
		part, err := pmr.NextPart()

		if err == io.EOF {
			break
		} else if err != nil {
			return textBody, htmlBody, embeddedFiles, err
		}

		contentType, params, err := mime.ParseMediaType(part.Header.Get("Content-Type"))
		if err != nil {
			return textBody, htmlBody, embeddedFiles, err
		}

		switch contentType {
		case contentTypeTextPlain:
			ppContent, err := readTextPart(part)
			if err != nil {
				return textBody, htmlBody, embeddedFiles, err
			}

			textBody += trimTrailingNewline(ppContent)
		case contentTypeTextHtml:
			ppContent, err := readTextPart(part)
			if err != nil {
				return textBody, htmlBody, embeddedFiles, err
			}

			htmlBody += trimTrailingNewline(ppContent)
		case contentTypeMultipartRelated:
			tb, hb, ef, err := parseMultipartRelated(part, params["boundary"])
			if err != nil {
				return textBody, htmlBody, embeddedFiles, err
			}

			htmlBody += hb
			textBody += tb
			embeddedFiles = append(embeddedFiles, ef...)
		default:
			if isEmbeddedFile(part) {
				ef, err := decodeEmbeddedFile(part)
				if err != nil {
					return textBody, htmlBody, embeddedFiles, err
				}

				embeddedFiles = append(embeddedFiles, ef)
			} else {
				return textBody, htmlBody, embeddedFiles, fmt.Errorf("Can't process multipart/alternative inner mime type: %s", contentType)
			}
		}
	}

	return textBody, htmlBody, embeddedFiles, err
}

func parseMultipartMixed(msg io.Reader, boundary string) (textBody, htmlBody string, attachments []Attachment, embeddedFiles []EmbeddedFile, err error) {
	mr := multipart.NewReader(msg, boundary)
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		} else if err != nil {
			return textBody, htmlBody, attachments, embeddedFiles, err
		}

		contentType, params, err := mime.ParseMediaType(part.Header.Get("Content-Type"))
		if err != nil {
			return textBody, htmlBody, attachments, embeddedFiles, err
		}

		if contentType == contentTypeMultipartAlternative {
			textBody, htmlBody, embeddedFiles, err = parseMultipartAlternative(part, params["boundary"])
			if err != nil {
				return textBody, htmlBody, attachments, embeddedFiles, err
			}
		} else if contentType == contentTypeMultipartRelated {
			textBody, htmlBody, embeddedFiles, err = parseMultipartRelated(part, params["boundary"])
			if err != nil {
				return textBody, htmlBody, attachments, embeddedFiles, err
			}
		} else if contentType == contentTypeTextPlain {
			ppContent, err := readTextPart(part)
			if err != nil {
				return textBody, htmlBody, attachments, embeddedFiles, err
			}

			textBody += trimTrailingNewline(ppContent)
		} else if contentType == contentTypeTextHtml {
			ppContent, err := readTextPart(part)
			if err != nil {
				return textBody, htmlBody, attachments, embeddedFiles, err
			}

			htmlBody += trimTrailingNewline(ppContent)
		} else {
			ok, err := isAttachment(part)

			if err != nil {
				return textBody, htmlBody, attachments, embeddedFiles, err
			}

			if ok {
				at, err := decodeAttachment(part)
				if err != nil {
					return textBody, htmlBody, attachments, embeddedFiles, err
				}

				attachments = append(attachments, at)
			} else {
				return textBody, htmlBody, attachments, embeddedFiles, fmt.Errorf("Unknown multipart/mixed nested mime type: %s", contentType)
			}
		}
	}

	return textBody, htmlBody, attachments, embeddedFiles, err
}

func decodeMimeSentence(s string) string {
	result := []string{}
	ss := strings.Split(s, " ")

	for _, word := range ss {
		w, err := wordDecoder.Decode(word)
		if err != nil {
			if len(result) == 0 {
				w = word
			} else {
				w = " " + word
			}
		}

		result = append(result, w)
	}

	return strings.Join(result, "")
}

func decodeHeaderMime(header mail.Header) mail.Header {
	parsedHeader := map[string][]string{}

	for headerName, headerData := range header {

		parsedHeaderData := []string{}
		for _, headerValue := range headerData {
			parsedHeaderData = append(parsedHeaderData, decodeMimeSentence(headerValue))
		}

		parsedHeader[headerName] = parsedHeaderData
	}

	return mail.Header(parsedHeader)
}

func isEmbeddedFile(part *multipart.Part) bool {
	return part.Header.Get("Content-Transfer-Encoding") != ""
}

func decodeEmbeddedFile(part *multipart.Part) (ef EmbeddedFile, err error) {
	cid := decodeMimeSentence(part.Header.Get("Content-Id"))
	decoded, err := decodeContent(part, part.Header.Get("Content-Transfer-Encoding"))
	if err != nil {
		return
	}

	ef.CID = strings.Trim(cid, "<>")
	ef.Data = decoded
	ef.ContentType = part.Header.Get("Content-Type")

	return
}

func getPartFilename(part *multipart.Part) (string, error) {
	if part.FileName() != "" {
		return part.FileName(), nil
	}

	contentType := part.Header.Get("Content-Type")

	_, attributes, err := mime.ParseMediaType(contentType)

	if err != nil {
		return "", fmt.Errorf("parsing content type header. %s", err)
	}

	return attributes["name"], nil
}

func isAttachment(part *multipart.Part) (bool, error) {
	filename, err := getPartFilename(part)

	if err != nil {
		return false, err
	}

	return filename != "", nil
}

func decodeAttachment(part *multipart.Part) (at Attachment, err error) {
	partFilename, err := getPartFilename(part)

	if err != nil {
		return
	}

	filename := decodeMimeSentence(partFilename)
	decoded, err := decodeContent(part, part.Header.Get("Content-Transfer-Encoding"))
	if err != nil {
		return
	}

	at.Filename = filename
	at.Data = decoded
	at.ContentType = strings.Split(part.Header.Get("Content-Type"), ";")[0]

	return
}

// readTextPart reads a text/plain or text/html part into a UTF-8 string,
// undoing its Content-Transfer-Encoding and charset first. Go's multipart
// reader transparently decodes quoted-printable and drops the header, but
// leaves base64 parts encoded, so reading one directly yields the base64 text
// rather than the message.
//
// A body is best-effort by design. Senders mislabel and truncate encodings, and
// failing the read would fail the whole email - taking its attachments with it,
// and permanently, since callers treat a parse error as unretryable. Junk prose
// is recoverable; a lost submission is not. Attachments stay strict, because
// silently handing back corrupt bytes is worse there than an error.
func readTextPart(part *multipart.Part) (string, error) {
	return readTextBody(part, textproto.MIMEHeader(part.Header))
}

// readTextBody is readTextPart's engine, taking the headers separately so a
// single-part message can reuse it with the message's own headers.
func readTextBody(body io.Reader, header textproto.MIMEHeader) (string, error) {
	raw, err := io.ReadAll(body)
	if err != nil {
		return "", err
	}

	decoded, err := decodeContentBytes(bytes.NewReader(raw), header.Get("Content-Transfer-Encoding"))
	if err != nil {
		decoded = raw
	}

	return decodeTextCharset(decoded, header.Get("Content-Type")), nil
}

// decodeTextCharset converts a decoded body from the charset its Content-Type
// declares into UTF-8. An absent, unknown or failing charset degrades to the
// bytes as they stand, with anything still invalid replaced, so a body is never
// the reason an email fails and downstream never receives invalid UTF-8.
func decodeTextCharset(content []byte, contentType string) string {
	charset := ""
	if _, params, err := mime.ParseMediaType(contentType); err == nil {
		charset = strings.ToLower(strings.TrimSpace(params["charset"]))
	}

	switch charset {
	case "", "utf-8", "utf8":
	default:
		if reader, err := getCharsetDecoder(charset, bytes.NewReader(content)); err == nil {
			if converted, err := io.ReadAll(reader); err == nil {
				content = converted
			}
		}
	}

	return strings.ToValidUTF8(string(content), "�")
}

// trimTrailingNewline drops one trailing line break, CRLF or LF. TrimRight is
// deliberately not used: it would strip every trailing newline and change the
// bodies the existing fixtures pin.
func trimTrailingNewline(s string) string {
	return strings.TrimSuffix(strings.TrimSuffix(s, "\n"), "\r")
}

func decodeContent(content io.Reader, encoding string) (io.Reader, error) {
	decoded, err := decodeContentBytes(content, encoding)
	if err != nil {
		return nil, err
	}

	return bytes.NewReader(decoded), nil
}

func decodeContentBytes(content io.Reader, encoding string) ([]byte, error) {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "base64":
		return io.ReadAll(base64.NewDecoder(base64.StdEncoding, content))
	case "quoted-printable":
		return io.ReadAll(quotedprintable.NewReader(content))
	// 8bit and binary are identity encodings; without them a part that declares
	// one would fail where it previously read through undecoded.
	case "7bit", "8bit", "binary", "":
		return io.ReadAll(content)
	default:
		return nil, fmt.Errorf("unknown encoding: %s", encoding)
	}
}

type headerParser struct {
	header *mail.Header
	err    error
}

func (hp *headerParser) parseAddress(s string) (ma *mail.Address) {
	if hp.err != nil {
		return nil
	}

	if strings.Trim(s, " \n") != "" {
		ma, hp.err = addressParser.Parse(s)

		return ma
	}

	return nil
}

func (hp *headerParser) parseAddressList(s string) (ma []*mail.Address) {
	if hp.err != nil {
		return
	}

	if strings.Trim(s, " \n") != "" {
		ma, hp.err = addressParser.ParseList(s)
		return
	}

	return
}

func (hp *headerParser) parseTime(s string) (t time.Time) {
	if hp.err != nil || s == "" {
		return
	}

	formats := []string{
		time.RFC1123Z,
		"Mon, 2 Jan 2006 15:04:05 -0700",
		time.RFC1123Z + " (MST)",
		"Mon, 2 Jan 2006 15:04:05 -0700 (MST)",
	}

	for _, format := range formats {
		t, hp.err = time.Parse(format, s)
		if hp.err == nil {
			return
		}
	}

	return
}

func (hp *headerParser) parseMessageId(s string) string {
	if hp.err != nil {
		return ""
	}

	return strings.Trim(s, "<> ")
}

func (hp *headerParser) parseMessageIdList(s string) (result []string) {
	if hp.err != nil {
		return
	}

	for _, p := range strings.Split(s, " ") {
		if strings.Trim(p, " \n") != "" {
			result = append(result, hp.parseMessageId(p))
		}
	}

	return
}

// Attachment with filename, content type and data (as a io.Reader)
type Attachment struct {
	Filename    string
	ContentType string
	Data        io.Reader
}

// EmbeddedFile with content id, content type and data (as a io.Reader)
type EmbeddedFile struct {
	CID         string
	ContentType string
	Data        io.Reader
}

// Email with fields for all the headers defined in RFC5322 with it's attachments and
type Email struct {
	Header mail.Header

	Subject    string
	Sender     *mail.Address
	From       []*mail.Address
	ReplyTo    []*mail.Address
	To         []*mail.Address
	Cc         []*mail.Address
	Bcc        []*mail.Address
	Date       time.Time
	MessageID  string
	InReplyTo  []string
	References []string

	ResentFrom      []*mail.Address
	ResentSender    *mail.Address
	ResentTo        []*mail.Address
	ResentDate      time.Time
	ResentCc        []*mail.Address
	ResentBcc       []*mail.Address
	ResentMessageID string

	ContentType string
	Content     io.Reader

	HTMLBody string
	TextBody string

	Attachments   []Attachment
	EmbeddedFiles []EmbeddedFile
}
