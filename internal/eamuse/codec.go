// Package eamuse reads and writes e-amusement XRPC bodies - just enough to read traffic
// and rewrite services.get.
//
// A body is rc4(lz77(kbin(xml))), each layer optional: X-Eamuse-Info selects rc4,
// X-Compress selects lz77, and the payload may be plain XML text instead of binary kbin.
package eamuse

import (
	"crypto/md5"
	"crypto/rc4"
	"encoding/hex"
	"fmt"
	"strings"
)

var internalKey, _ = hex.DecodeString("69d74627d985ee2187161570d08d93b12455035b6df0d8205df5")

// Crypt encrypts or decrypts (rc4 is symmetric) with the key an X-Eamuse-Info value names.
func Crypt(info string, data []byte) ([]byte, error) {
	parts := strings.Split(info, "-")
	if len(parts) != 3 {
		return nil, fmt.Errorf("bad X-Eamuse-Info %q", info)
	}
	seconds, err1 := hex.DecodeString(parts[1])
	salt, err2 := hex.DecodeString(parts[2])
	if err1 != nil || err2 != nil {
		return nil, fmt.Errorf("bad X-Eamuse-Info %q", info)
	}
	key := md5.Sum(append(append(seconds, salt...), internalKey...))
	c, _ := rc4.NewCipher(key[:])
	out := make([]byte, len(data))
	c.XORKeyStream(out, data)
	return out, nil
}

// LZ77Decode undoes the e-amusement lz77 (4 KiB window, 8 flags per control byte).
func LZ77Decode(data []byte) []byte {
	out := make([]byte, 0, len(data)*2)
	var window [0x1000]byte
	cursor, pos := 0, 0
	for pos < len(data) {
		flags := data[pos]
		pos++
		for bit := 0; bit < 8; bit++ {
			if flags>>bit&1 != 0 {
				if pos >= len(data) {
					return out
				}
				b := data[pos]
				pos++
				out = append(out, b)
				window[cursor] = b
				cursor = (cursor + 1) & 0xFFF
				continue
			}
			if pos+1 >= len(data) {
				return out
			}
			word := int(data[pos])<<8 | int(data[pos+1])
			pos += 2
			if word == 0 {
				return out
			}
			src := (cursor - word>>4) & 0xFFF
			for n := word&0xF + 3; n > 0; n-- {
				b := window[src]
				out = append(out, b)
				window[cursor] = b
				cursor = (cursor + 1) & 0xFFF
				src = (src + 1) & 0xFFF
			}
		}
	}
	return out
}

// Unwrap removes rc4 and lz77, leaving kbin or XML text.
func Unwrap(body []byte, info, compress string) ([]byte, error) {
	if info != "" {
		var err error
		if body, err = Crypt(info, body); err != nil {
			return nil, err
		}
	}
	if strings.EqualFold(compress, "lz77") {
		body = LZ77Decode(body)
	}
	return body, nil
}

// Decode unwraps a body and parses it; binary tells whether it was kbin.
func Decode(body []byte, info, compress string) (root *Node, binary bool, err error) {
	plain, err := Unwrap(body, info, compress)
	if err != nil {
		return nil, false, err
	}
	if IsBinary(plain) {
		root, err = DecodeBinary(plain)
		return root, true, err
	}
	root, err = ParseXML(plain)
	return root, false, err
}

// Encode writes a document uncompressed, encrypted with the same key if info is set;
// it returns the body and the headers that describe it.
func Encode(root *Node, binary bool, info string) ([]byte, map[string]string, error) {
	var body []byte
	if binary {
		var err error
		if body, err = EncodeBinary(root); err != nil {
			return nil, nil, err
		}
	} else {
		body = root.Document()
	}
	headers := map[string]string{"X-Compress": "none"}
	if info != "" {
		var err error
		if body, err = Crypt(info, body); err != nil {
			return nil, nil, err
		}
		headers["X-Eamuse-Info"] = info
	}
	return body, headers, nil
}
