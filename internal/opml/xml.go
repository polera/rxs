package opml

import (
	"encoding/xml"
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"
)

// encoding/xml only extracts selected declaration attributes; check their order,
// separators and values as well. Like the decoder, import supports XML 1.0/UTF-8.
var importDeclaration = regexp.MustCompile(`^version[ \t\r\n]*=[ \t\r\n]*("1\.0"|'1\.0')` +
	`([ \t\r\n]+encoding[ \t\r\n]*=[ \t\r\n]*("(?i:UTF-8)"|'(?i:UTF-8)'))?` +
	`([ \t\r\n]+standalone[ \t\r\n]*=[ \t\r\n]*("(yes|no)"|'(yes|no)'))?[ \t\r\n]*$`)

// Support common external OPML DTD declarations, not internal subsets. Public
// identifiers use XML's PubidChar set; system identifiers are quoted literals.
var importDoctype = regexp.MustCompile(`^DOCTYPE[ \t\r\n]+opml(` +
	`[ \t\r\n]+SYSTEM[ \t\r\n]+("[^"]*"|'[^']*')|` +
	`[ \t\r\n]+PUBLIC[ \t\r\n]+("[ \r\na-zA-Z0-9'()+,./:=?;!*#@$_%\-]*"|'[ \r\na-zA-Z0-9()+,./:=?;!*#@$_%\-]*')` +
	`[ \t\r\n]+("[^"]*"|'[^']*'))?[ \t\r\n]*$`)

// Validate tokens even when DecodeElement would otherwise discard them. RawToken
// leaves namespace resolution and element matching to the outer token decoder.
type importTokens struct {
	*xml.Decoder
	rootSeen bool
}

func (r *importTokens) Token() (xml.Token, error) {
	offset := r.InputOffset()
	token, err := r.RawToken()
	if err != nil {
		return nil, err
	}
	var data []byte
	switch token := token.(type) {
	case xml.StartElement:
		r.rootSeen = true
	case xml.Comment:
		data = token
	case xml.ProcInst:
		data = token.Inst
		// RawToken strips leading XML whitespace from Inst. A nonempty Inst
		// needs at least one separator byte beyond <?, the target, and ?>.
		if len(data) > 0 && r.InputOffset()-offset <= int64(len(token.Target)+len(data)+4) {
			return nil, errors.New("processing instruction data requires whitespace after its target")
		}
		if strings.EqualFold(token.Target, "xml") && (token.Target != "xml" || offset != 0 || !importDeclaration.Match(data)) {
			return nil, errors.New("invalid XML declaration")
		}
	case xml.Directive:
		data = token
		// RawToken removes comments embedded in directives. Reject those too,
		// rather than letting the supported DOCTYPE grammar match altered text.
		if r.rootSeen || r.InputOffset()-offset != int64(len(data)+3) || !importDoctype.Match(data) {
			return nil, errors.New("unsupported DOCTYPE: expected opml with an optional SYSTEM or PUBLIC identifier, without an internal subset")
		}
	}
	// encoding/xml checks CharData/attributes but not comment, PI or directive
	// payloads. Tokens assemble complete UTF-8 sequences across reader chunks.
	for len(data) > 0 {
		ch, size := utf8.DecodeRune(data)
		if ch == utf8.RuneError && size == 1 || !(ch == '\t' || ch == '\n' || ch == '\r' ||
			ch >= 0x20 && ch <= 0xD7FF || ch >= 0xE000 && ch <= 0xFFFD || ch >= 0x10000 && ch <= 0x10FFFF) {
			return nil, errors.New("invalid UTF-8 or XML character")
		}
		data = data[size:]
	}
	return token, nil
}
