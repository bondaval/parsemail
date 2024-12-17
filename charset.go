package parsemail

import (
	"fmt"
	"io"
	"mime"
	"net/mail"
	"strings"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/transform"
)

type charsetError string

func (e charsetError) Error() string {
	return fmt.Sprintf("charset not supported: %q", string(e))
}

type charsetDecoder func(input io.Reader) (io.Reader, error)

// charsetDecoderMap to store decoder functions for different charsets
var charsetDecoderMap = map[string]charsetDecoder{
	"iso-8859-2": func(input io.Reader) (io.Reader, error) {
		return transform.NewReader(input, charmap.ISO8859_2.NewDecoder()), nil
	},
}

// Function to get the decoder for a given charset
func getCharsetDecoder(charset string, input io.Reader) (io.Reader, error) {
	decoder, exists := charsetDecoderMap[strings.ToLower(charset)]
	if !exists {
		return nil, charsetError(charset)
	}
	return decoder(input)
}

// AddressParser with extendable charset handling
var addressParser = mail.AddressParser{
	WordDecoder: &mime.WordDecoder{
		CharsetReader: getCharsetDecoder,
	},
}
