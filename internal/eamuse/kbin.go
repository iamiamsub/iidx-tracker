package eamuse

// Binary XML ("kbin"), ported from kbinxml by mon (MIT, see THIRD_PARTY_NOTICES.txt).
//
// Layout: A0 <42 six-bit names | 45 raw names> <encoding> <~encoding> u32 node-size,
// the node stream, u32 data-size, the data stream. Big-endian. Node values live in the
// data stream; 1- and 2-byte scalars are packed into shared 4-byte slots.

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/htmlindex"
	"golang.org/x/text/encoding/japanese"
)

const (
	sigBinary       = 0xA0
	sigCompressed   = 0x42 // six-bit packed names
	sigUncompressed = 0x45 // length-prefixed names
	typeVoid        = 1
	typeBin         = 10
	typeStr         = 11
	typeIP4         = 12
	typeAttr        = 46
	typeNodeEnd     = 190
	typeSectionEnd  = 191
	arrayFlag       = 64
)

type format struct {
	name   string
	size   int // bytes per scalar
	count  int // scalars per value; -1 for bin and str
	signed bool
	float  bool
}

var formats = map[byte]format{}
var typeIDs = map[string]byte{"nodeStart": 1, "nodeEnd": typeNodeEnd, "endSection": typeSectionEnd}

func init() {
	add := func(id byte, size, count int, signed, float bool, names ...string) {
		formats[id] = format{names[0], size, count, signed, float}
		for _, n := range names {
			typeIDs[n] = id
		}
	}
	add(1, 0, 0, false, false, "void")
	add(2, 1, 1, true, false, "s8")
	add(3, 1, 1, false, false, "u8")
	add(4, 2, 1, true, false, "s16")
	add(5, 2, 1, false, false, "u16")
	add(6, 4, 1, true, false, "s32")
	add(7, 4, 1, false, false, "u32")
	add(8, 8, 1, true, false, "s64")
	add(9, 8, 1, false, false, "u64")
	add(10, 1, -1, false, false, "bin", "binary")
	add(11, 1, -1, false, false, "str", "string")
	add(12, 4, 1, false, false, "ip4")
	add(13, 4, 1, false, false, "time")
	add(14, 4, 1, true, true, "float", "f")
	add(15, 8, 1, true, true, "double", "d")
	for i, sz := range []int{1, 1, 2, 2, 4, 4, 8, 8} { // 2s8 .. 4u64
		signed := i%2 == 0
		base := []string{"s8", "u8", "s16", "u16", "s32", "u32", "s64", "u64"}[i]
		add(byte(16+i), sz, 2, signed, false, "2"+base)
		add(byte(26+i), sz, 3, signed, false, "3"+base)
		add(byte(36+i), sz, 4, signed, false, "4"+base)
	}
	add(24, 4, 2, true, true, "2f")
	add(25, 8, 2, true, true, "2d", "vd")
	add(34, 4, 3, true, true, "3f")
	add(35, 8, 3, true, true, "3d")
	add(44, 4, 4, true, true, "4f", "vf")
	add(45, 8, 4, true, true, "4d")
	add(46, 0, 0, false, false, "attr")
	add(48, 1, 16, true, false, "vs8")
	add(49, 1, 16, false, false, "vu8")
	add(50, 2, 8, true, false, "vs16")
	add(51, 2, 8, false, false, "vu16")
	add(52, 1, 1, true, false, "bool", "b")
	add(53, 1, 2, true, false, "2b")
	add(54, 1, 3, true, false, "3b")
	add(55, 1, 4, true, false, "4b")
	add(56, 1, 16, true, false, "vb")
	// aliases kbinxml accepts on input
	typeIDs["2s64"], typeIDs["vs64"], typeIDs["2u64"], typeIDs["vu64"] = 22, 22, 23, 23
	typeIDs["4s32"], typeIDs["vs32"], typeIDs["4u32"], typeIDs["vu32"] = 40, 40, 41, 41
}

// encodings by header byte; 0x00 and 0x80 are both Shift-JIS (cp932)
var encodings = map[byte]encoding.Encoding{
	0x00: japanese.ShiftJIS, 0x20: charmap.ISO8859_1, 0x40: charmap.ISO8859_1,
	0x60: japanese.EUCJP, 0x80: japanese.ShiftJIS, 0xA0: encoding.Nop,
}

func charsetReader(label string, r io.Reader) (io.Reader, error) {
	enc, err := htmlindex.Get(label)
	if err != nil {
		return nil, fmt.Errorf("unsupported charset %q", label)
	}
	return enc.NewDecoder().Reader(r), nil
}

// IsBinary reports whether data is binary XML.
func IsBinary(data []byte) bool {
	return len(data) >= 2 && data[0] == sigBinary && (data[1] == sigCompressed || data[1] == sigUncompressed)
}

const sixbitChars = "0123456789:ABCDEFGHIJKLMNOPQRSTUVWXYZ_abcdefghijklmnopqrstuvwxyz"

var errTruncated = errors.New("kbin: truncated")

type reader struct {
	b   []byte
	off int
}

func (r *reader) bytes(n int) []byte {
	if n < 0 || r.off+n > len(r.b) {
		panic(errTruncated)
	}
	v := r.b[r.off : r.off+n]
	r.off += n
	return v
}

func (r *reader) u8() byte    { return r.bytes(1)[0] }
func (r *reader) u32() uint32 { return binary.BigEndian.Uint32(r.bytes(4)) }
func (r *reader) align()      { r.off = (r.off + 3) &^ 3 }

// DecodeBinary parses binary XML; the document's root element is returned.
func DecodeBinary(data []byte) (root *Node, err error) {
	defer func() {
		if p := recover(); p != nil {
			if e, ok := p.(error); ok && errors.Is(e, errTruncated) {
				root, err = nil, e
				return
			}
			panic(p)
		}
	}()
	if !IsBinary(data) || len(data) < 8 {
		return nil, errors.New("kbin: not binary XML")
	}
	sixbit := data[1] == sigCompressed
	enc, ok := encodings[data[2]]
	if !ok || data[3] != 0xFF^data[2] {
		return nil, fmt.Errorf("kbin: bad encoding byte %#x", data[2])
	}
	nodes := &reader{b: data, off: 4}
	nodeEnd := int(nodes.u32()) + 8
	if nodeEnd > len(data) {
		return nil, errTruncated
	}
	nodes.b = data[:nodeEnd]
	dat := &reader{b: data, off: nodeEnd}
	dat.u32() // data size
	byteOff, wordOff := nodeEnd, nodeEnd

	text := func(raw []byte) string { return decodeString(enc, raw) }
	name := func() string {
		if sixbit {
			n := int(nodes.u8())
			packed := nodes.bytes((n*6 + 7) / 8)
			out := make([]byte, n)
			for i := 0; i < n; i++ {
				bit := i * 6
				v := (int(packed[bit/8])<<8 | int(at(packed, bit/8+1))) >> (10 - bit%8) & 0x3F
				out[i] = sixbitChars[v]
			}
			return string(out)
		}
		n := int(nodes.u8()&^64) + 1
		return text(nodes.bytes(n))
	}
	// the scalars of a non-array value: 1 and 2 byte values are packed into shared 4-byte slots
	grabAligned := func(size int) []byte {
		if byteOff%4 == 0 {
			byteOff = dat.off
		}
		if wordOff%4 == 0 {
			wordOff = dat.off
		}
		var v []byte
		switch size {
		case 1:
			v = (&reader{b: data, off: byteOff}).bytes(1)
			byteOff++
		case 2:
			v = (&reader{b: data, off: wordOff}).bytes(2)
			wordOff += 2
		default:
			v = dat.bytes(size)
			dat.align()
		}
		if trailing := max(byteOff, wordOff); dat.off < trailing {
			dat.off = trailing
			dat.align()
		}
		return v
	}

	top := &Node{Name: "root"}
	stack := []*Node{top}
	for nodes.off < len(nodes.b) {
		for nodes.off < len(nodes.b) && nodes.b[nodes.off] == 0 {
			nodes.off++
		}
		if nodes.off >= len(nodes.b) {
			break
		}
		t := nodes.u8()
		isArray := t&arrayFlag != 0
		t &^= arrayFlag
		if t == typeSectionEnd {
			break
		}
		if t == typeNodeEnd {
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
			continue
		}
		nm := name()
		cur := stack[len(stack)-1]
		if t == typeAttr {
			size := int(int32(dat.u32()))
			raw := dat.bytes(size)
			dat.align()
			if len(raw) > 0 {
				raw = raw[:len(raw)-1]
			}
			cur.Attrs = append(cur.Attrs, Attr{nm, text(raw)})
			continue
		}
		f, ok := formats[t]
		if !ok {
			return nil, fmt.Errorf("kbin: unknown node type %d", t)
		}
		if nm != "" && nm[0] >= '0' && nm[0] <= '9' {
			nm = "_" + nm // not a valid XML name (kbinxml convert_illegal_things)
		}
		child := &Node{Name: nm}
		cur.Children = append(cur.Children, child)
		stack = append(stack, child)
		if t == typeVoid {
			continue
		}
		child.Attrs = append(child.Attrs, Attr{"__type", f.name})
		count, arrayCount := f.count, 1
		if count == -1 {
			count = int(dat.u32())
			isArray = true
		} else if isArray {
			arrayCount = int(dat.u32()) / (f.size * count)
			child.Attrs = append(child.Attrs, Attr{"__count", strconv.Itoa(arrayCount)})
		}
		total := arrayCount * count
		var raw []byte
		if isArray {
			raw = dat.bytes(total * f.size)
			dat.align()
		} else {
			raw = grabAligned(total * f.size)
		}
		switch t {
		case typeBin:
			child.Attrs = append(child.Attrs, Attr{"__size", strconv.Itoa(total)})
			child.Text = hex.EncodeToString(raw)
		case typeStr:
			if len(raw) > 0 {
				raw = raw[:len(raw)-1]
			}
			child.Text = strings.Trim(text(raw), "\x00")
		default:
			child.Text = scalarsText(t, f, raw)
		}
	}
	if len(top.Children) == 0 {
		return nil, errors.New("kbin: empty document")
	}
	return top.Children[0], nil
}

func at(b []byte, i int) byte {
	if i < len(b) {
		return b[i]
	}
	return 0
}

// decodeString decodes like kbinxml with convert_illegal_things: Shift-JIS that is not valid
// Shift-JIS but is valid UTF-8 is read as UTF-8.
func decodeString(enc encoding.Encoding, raw []byte) string {
	if enc == encoding.Nop {
		return string(raw)
	}
	s, err := enc.NewDecoder().Bytes(raw)
	if err != nil || (strings.ContainsRune(string(s), utf8.RuneError) && utf8.Valid(raw)) {
		return string(raw)
	}
	return string(s)
}

func scalarsText(t byte, f format, raw []byte) string {
	parts := make([]string, 0, len(raw)/f.size)
	for i := 0; i+f.size <= len(raw); i += f.size {
		v := raw[i : i+f.size]
		var s string
		switch {
		case t == typeIP4:
			s = fmt.Sprintf("%d.%d.%d.%d", v[0], v[1], v[2], v[3])
		case f.float && f.size == 4:
			s = strconv.FormatFloat(float64(math.Float32frombits(binary.BigEndian.Uint32(v))), 'f', 6, 64)
		case f.float:
			s = strconv.FormatFloat(math.Float64frombits(binary.BigEndian.Uint64(v)), 'f', 6, 64)
		default:
			var u uint64
			for _, c := range v {
				u = u<<8 | uint64(c)
			}
			if f.signed {
				shift := 64 - 8*f.size
				s = strconv.FormatInt(int64(u<<shift)>>shift, 10)
			} else {
				s = strconv.FormatUint(u, 10)
			}
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, " ")
}

type writer struct {
	nodes, data      []byte
	byteOff, wordOff int
	enc              encoding.Encoding
}

// EncodeBinary writes n as binary XML with Shift-JIS strings and six-bit names, byte for
// byte what kbinxml's to_binary() produces.
func EncodeBinary(n *Node) ([]byte, error) {
	w := &writer{enc: japanese.ShiftJIS}
	if err := w.node(n); err != nil {
		return nil, err
	}
	w.nodes = append(w.nodes, typeSectionEnd|arrayFlag)
	for len(w.nodes)%4 != 0 {
		w.nodes = append(w.nodes, 0)
	}
	out := []byte{sigBinary, sigCompressed, 0x80, 0xFF ^ 0x80}
	out = binary.BigEndian.AppendUint32(out, uint32(len(w.nodes)))
	out = append(out, w.nodes...)
	out = binary.BigEndian.AppendUint32(out, uint32(len(w.data)))
	return append(out, w.data...), nil
}

func (w *writer) name(s string) error {
	bits := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		p := strings.IndexByte(sixbitChars, s[i])
		if p < 0 {
			return fmt.Errorf("kbin: %q cannot be a six-bit name", s)
		}
		bits = append(bits, byte(p))
	}
	w.nodes = append(w.nodes, byte(len(s)))
	packed := make([]byte, (len(s)*6+7)/8)
	for i, v := range bits {
		for k := 0; k < 6; k++ {
			if v>>(5-k)&1 != 0 {
				bit := i*6 + k
				packed[bit/8] |= 0x80 >> (bit % 8)
			}
		}
	}
	w.nodes = append(w.nodes, packed...)
	return nil
}

func (w *writer) align() {
	for len(w.data)%4 != 0 {
		w.data = append(w.data, 0)
	}
}

func (w *writer) str(s string) ([]byte, error) {
	b, err := w.enc.NewEncoder().Bytes([]byte(s))
	if err != nil { // kbinxml encodes with errors="replace"
		b, _ = encoding.ReplaceUnsupported(w.enc.NewEncoder()).Bytes([]byte(s))
	}
	return append(b, 0), nil
}

func (w *writer) node(n *Node) error {
	typeName, typed := n.Attr("__type")
	if !typed {
		typeName = "void"
		if strings.TrimSpace(n.Text) != "" {
			typeName = "str"
		}
	}
	t, ok := typeIDs[typeName]
	if !ok {
		return fmt.Errorf("kbin: unknown type %q", typeName)
	}
	countAttr, isArray := n.Attr("__count")
	head := t
	if isArray {
		head |= arrayFlag
	}
	w.nodes = append(w.nodes, head)
	if err := w.name(n.Name); err != nil {
		return err
	}
	if t != typeVoid {
		f := formats[t]
		var raw []byte
		switch t {
		case typeBin:
			b, err := hex.DecodeString(strings.TrimSpace(n.Text))
			if err != nil {
				return fmt.Errorf("kbin: %s: %v", n.Name, err)
			}
			raw = b
		case typeStr:
			raw, _ = w.str(n.Text)
		default:
			fields := strings.Fields(n.Text)
			if isArray {
				want, _ := strconv.Atoi(countAttr)
				if len(fields) != want*f.count {
					return fmt.Errorf("kbin: %s: array length does not match __count", n.Name)
				}
			}
			for _, s := range fields {
				b, err := scalarBytes(t, f, s)
				if err != nil {
					return fmt.Errorf("kbin: %s: %v", n.Name, err)
				}
				raw = append(raw, b...)
			}
		}
		if isArray || f.count == -1 {
			w.data = binary.BigEndian.AppendUint32(w.data, uint32(len(raw)))
			w.data = append(w.data, raw...)
			w.align()
		} else {
			w.appendAligned(raw)
		}
	}
	attrs := make([]Attr, 0, len(n.Attrs))
	for _, a := range n.Attrs {
		if a.Name != "__type" && a.Name != "__size" && a.Name != "__count" {
			attrs = append(attrs, a)
		}
	}
	sort.SliceStable(attrs, func(i, j int) bool { return attrs[i].Name < attrs[j].Name })
	for _, a := range attrs {
		b, _ := w.str(a.Value)
		w.data = binary.BigEndian.AppendUint32(w.data, uint32(len(b)))
		w.data = append(w.data, b...)
		w.align()
		w.nodes = append(w.nodes, typeAttr)
		if err := w.name(a.Name); err != nil {
			return err
		}
	}
	for _, c := range n.Children {
		if err := w.node(c); err != nil {
			return err
		}
	}
	w.nodes = append(w.nodes, typeNodeEnd|arrayFlag)
	return nil
}

func (w *writer) appendAligned(raw []byte) {
	if w.byteOff%4 == 0 {
		w.byteOff = len(w.data)
	}
	if w.wordOff%4 == 0 {
		w.wordOff = len(w.data)
	}
	switch len(raw) {
	case 1:
		if w.byteOff%4 == 0 {
			w.data = append(w.data, 0, 0, 0, 0)
		}
		w.data[w.byteOff] = raw[0]
		w.byteOff++
	case 2:
		if w.wordOff%4 == 0 {
			w.data = append(w.data, 0, 0, 0, 0)
		}
		copy(w.data[w.wordOff:], raw)
		w.wordOff += 2
	default:
		w.data = append(w.data, raw...)
		w.align()
	}
}

func scalarBytes(t byte, f format, s string) ([]byte, error) {
	b := make([]byte, f.size)
	switch {
	case t == typeIP4:
		parts := strings.Split(s, ".")
		if len(parts) != 4 {
			return nil, fmt.Errorf("bad ip4 %q", s)
		}
		for i, p := range parts {
			v, err := strconv.ParseUint(p, 10, 8)
			if err != nil {
				return nil, err
			}
			b[i] = byte(v)
		}
	case f.float:
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil, err
		}
		if f.size == 4 {
			binary.BigEndian.PutUint32(b, math.Float32bits(float32(v)))
		} else {
			binary.BigEndian.PutUint64(b, math.Float64bits(v))
		}
	default:
		var u uint64
		if f.signed {
			v, err := strconv.ParseInt(s, 10, 8*f.size)
			if err != nil {
				return nil, err
			}
			u = uint64(v)
		} else {
			v, err := strconv.ParseUint(s, 10, 8*f.size)
			if err != nil {
				return nil, err
			}
			u = v
		}
		for i := f.size - 1; i >= 0; i-- {
			b[i] = byte(u)
			u >>= 8
		}
	}
	return b, nil
}
