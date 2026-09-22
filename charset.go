package parsemail

import (
	"fmt"
	"io"
	"mime"
	"net/mail"

	"golang.org/x/text/encoding/htmlindex"
	"golang.org/x/text/transform"
)

type charsetError string

func (e charsetError) Error() string {
	return fmt.Sprintf("charset not supported: %q", string(e))
}

// getCharsetDecoder resolves a declared charset to a reader that yields UTF-8.
//
// htmlindex rather than ianaindex, because it indexes the WHATWG label set -
// which is what mail clients actually emit, including cp1252, utf8, iso8859-1
// and gb2312 - and every label it knows maps to an encoding that is
// implemented. ianaindex registers names it cannot decode and returns a nil
// Encoding with a nil error for them, gb2312 among them, and NewDecoder on that
// nil panics.
//
// Enumerating charsets by hand does not work here: a mail client picks from the
// sending machine's locale, so the set is as wide as the senders are.
func getCharsetDecoder(charset string, input io.Reader) (io.Reader, error) {
	// htmlindex.Get lower-cases and trims the name itself, so callers need not.
	encoding, err := htmlindex.Get(charset)
	// The nil check is unreachable via htmlindex today. It stays because the
	// cost of being wrong is a panic, not a bad decode.
	if err != nil || encoding == nil {
		return nil, charsetError(charset)
	}

	return transform.NewReader(input, encoding.NewDecoder()), nil
}

// wordDecoder decodes RFC 2047 encoded-words (Subject, display names,
// attachment filenames). Without the CharsetReader it silently handles only
// utf-8, iso-8859-1 and us-ascii, leaving everything else as raw
// "=?windows-1254?Q?...?=" text.
var wordDecoder = &mime.WordDecoder{CharsetReader: getCharsetDecoder}

// AddressParser with extendable charset handling
var addressParser = mail.AddressParser{
	WordDecoder: wordDecoder,
}
