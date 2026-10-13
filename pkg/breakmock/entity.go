// Copyright 2026 Breaking-IT. Licensed under Apache-2.0; see LICENSE.
package breakmock

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/ianaindex"
	"golang.org/x/text/encoding/unicode"
	"path/filepath"
	"strings"
	"unicode/utf8"
	"unsafe"
)

type resolvedEntity struct {
	data        []byte
	binary      bool
	charset     string
	fromFile    bool
	compression string
}

func entityCharset(name string) (encoding.Encoding, error) {
	switch strings.ToUpper(name) {
	case "", "UTF-8", "UTF8":
		return nil, nil
	case "ISO-8859-1", "ISO8859_1", "LATIN1":
		return charmap.ISO8859_1, nil
	case "UTF-16", "UTF16":
		return unicode.UTF16(unicode.BigEndian, unicode.UseBOM), nil
	case "UTF-16BE":
		return unicode.UTF16(unicode.BigEndian, unicode.IgnoreBOM), nil
	case "UTF-16LE":
		return unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM), nil
	}
	e, err := ianaindex.MIME.Encoding(name)
	if e == nil && err == nil {
		err = fmt.Errorf("unknown charset %s", name)
	}
	return e, err
}
func entityEncode(text, charset string) ([]byte, error) {
	e, err := entityCharset(charset)
	if err != nil {
		return nil, err
	}
	if e == nil {
		return []byte(text), nil
	}
	encoded, err := e.NewEncoder().Bytes([]byte(text))
	if err == nil {
		return encoded, nil
	}
	// Java CharsetEncoder replaces unmappable code points with '?' (Go's
	// ReplaceUnsupported uses SUB for several legacy encodings).
	var cleaned strings.Builder
	for _, r := range text {
		if _, failure := e.NewEncoder().String(string(r)); failure != nil {
			cleaned.WriteByte('?')
		} else {
			cleaned.WriteRune(r)
		}
	}
	return e.NewEncoder().Bytes([]byte(cleaned.String()))
}
func (e resolvedEntity) text() (string, error) {
	codec, err := entityCharset(e.charset)
	if err != nil {
		return "", err
	}
	if codec == nil {
		if utf8.Valid(e.data) {
			return readOnlyString(e.data), nil
		}
		return strings.ToValidUTF8(string(e.data), "\ufffd"), nil
	}
	data, err := codec.NewDecoder().Bytes(e.data)
	return string(data), err
}
func resolveBodyEntity(value any, root string) (result resolvedEntity, resultErr error) {
	defer func() {
		if result.compression == "" && !result.fromFile && len(result.data) >= 2 && result.data[0] == 0x1f && result.data[1] == 0x8b {
			result.compression = "gzip"
		}
	}()
	if text, ok := value.(string); ok {
		result.data = readOnlyBytes(text)
		return result, nil
	}
	object, ok := value.(map[string]any)
	if !ok {
		return result, nil
	}
	result.charset = bmString(object["charset"])
	result.compression = strings.ToLower(bmString(object["compression"]))
	format := strings.ToLower(bmString(object["format"]))
	if format == "" {
		format = strings.ToLower(bmString(object["encoding"]))
	}
	result.binary = format == "binary" || (format == "" && object["base64Data"] != nil)
	if text, ok := object["data"].(string); ok {
		var err error
		result.data, err = entityEncode(text, result.charset)
		return result, err
	}
	if data, ok := object["data"].(map[string]any); ok {
		var err error
		result.charset = ""
		result.binary = false
		result.data, err = marshalJSON(data)
		return result, err
	}
	if data, ok := object["data"].(json.RawMessage); ok {
		result.data = data
		result.charset = ""
		result.binary = false
		return result, nil
	}
	if encoded, ok := object["base64Data"].(string); ok {
		var err error
		result.data, err = base64.StdEncoding.DecodeString(encoded)
		return result, err
	}
	name, fromFile := object["filePath"].(string)
	fromRef := object["dataRef"] != nil
	if fromRef && object["dataStore"] == "__files" {
		name = bmString(object["dataRef"])
		fromFile = true
	}
	if fromFile {
		result.fromFile = true
		var err error
		result.data, err = readBodyFile(filepath.Join(root, "__files"), name)
		if err == nil {
			return result, nil
		}
		if !fromRef {
			return result, &wireEntityError{"com.github.tomakehurst.wiremock.admin.NotFoundException: Not found in blob store: " + name}
		}
	}
	if fromRef {
		return result, &wireEntityError{`java.lang.NullPointerException: Cannot invoke "com.github.tomakehurst.wiremock.common.InputStreamSource.getStream()" because the return value of "com.github.tomakehurst.wiremock.common.entity.Entity.getStreamSource()" is null`}
	}
	return result, nil
}

// readOnlyBytes and readOnlyString convert without copying. Resolved bodies
// (inline text, cached body files, stored jsonBody, rendered templates) are only
// written, compressed, copied or sliced; they are never modified in place.
func readOnlyBytes(s string) []byte {
	if s == "" {
		return []byte{}
	}
	return unsafe.Slice(unsafe.StringData(s), len(s))
}
func readOnlyString(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return unsafe.String(&b[0], len(b))
}
