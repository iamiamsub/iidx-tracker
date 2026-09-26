package eamuse

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"strings"
)

// Attr is one XML attribute. Typed values keep kbinxml's __type, __count and __size here.
type Attr struct{ Name, Value string }

// Node is one element, shaped the way kbinxml exposes it: a typed value carries its
// __type (and __count / __size) as attributes and its value as text: numbers separated
// by spaces, bin as hex, str as is.
type Node struct {
	Name     string
	Attrs    []Attr
	Text     string
	Children []*Node
}

// Attr returns the attribute value and whether it is present.
func (n *Node) Attr(name string) (string, bool) {
	if n == nil {
		return "", false
	}
	for _, a := range n.Attrs {
		if a.Name == name {
			return a.Value, true
		}
	}
	return "", false
}

// Get returns the attribute value, or "" when it is missing.
func (n *Node) Get(name string) string {
	v, _ := n.Attr(name)
	return v
}

// Set replaces or appends an attribute.
func (n *Node) Set(name, value string) {
	for i := range n.Attrs {
		if n.Attrs[i].Name == name {
			n.Attrs[i].Value = value
			return
		}
	}
	n.Attrs = append(n.Attrs, Attr{name, value})
}

// Find returns the first element matching a slash-separated child path ("a/b"), like ElementTree.find.
func (n *Node) Find(path string) *Node {
	if found := n.FindAll(path); len(found) > 0 {
		return found[0]
	}
	return nil
}

// FindAll returns every element matching a slash-separated child path, in document order.
func (n *Node) FindAll(path string) []*Node {
	if n == nil {
		return nil
	}
	cur := []*Node{n}
	for _, step := range strings.Split(path, "/") {
		var next []*Node
		for _, c := range cur {
			for _, k := range c.Children {
				if k.Name == step {
					next = append(next, k)
				}
			}
		}
		cur = next
	}
	return cur
}

// FindText returns the text of the first match and whether there was one, like ElementTree.findtext.
func (n *Node) FindText(path string) (string, bool) {
	if f := n.Find(path); f != nil {
		return f.Text, true
	}
	return "", false
}

// Iter returns n and its descendants named name, in document order.
func (n *Node) Iter(name string) []*Node {
	var out []*Node
	var walk func(*Node)
	walk = func(x *Node) {
		if x.Name == name {
			out = append(out, x)
		}
		for _, c := range x.Children {
			walk(c)
		}
	}
	if n != nil {
		walk(n)
	}
	return out
}

// ParseXML reads an XML document; the root element is returned. Only the text before
// the first child is kept (what lxml calls .text).
func ParseXML(data []byte) (*Node, error) {
	d := xml.NewDecoder(bytes.NewReader(data))
	d.Strict = false
	d.CharsetReader = func(label string, r io.Reader) (io.Reader, error) { return charsetReader(label, r) }
	var stack []*Node
	var root *Node
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			n := &Node{Name: qname(t.Name)}
			for _, a := range t.Attr {
				if a.Name.Space == "xmlns" || a.Name.Local == "xmlns" {
					continue
				}
				n.Attrs = append(n.Attrs, Attr{qname(a.Name), a.Value})
			}
			if len(stack) > 0 {
				p := stack[len(stack)-1]
				p.Children = append(p.Children, n)
			} else if root == nil {
				root = n
			}
			stack = append(stack, n)
		case xml.EndElement:
			if len(stack) > 0 {
				top := stack[len(stack)-1]
				if len(top.Children) > 0 && strings.TrimSpace(top.Text) == "" {
					top.Text = "" // indentation, not a value
				}
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			if len(stack) > 0 {
				if top := stack[len(stack)-1]; len(top.Children) == 0 {
					top.Text += string(t)
				}
			}
		}
	}
	if root == nil {
		return nil, errors.New("xml: no root element")
	}
	return root, nil
}

func qname(n xml.Name) string {
	if n.Space != "" {
		return n.Space + ":" + n.Local
	}
	return n.Local
}

// XML writes the element as XML text without a declaration (like lxml's tostring).
func (n *Node) XML() []byte {
	var b bytes.Buffer
	n.writeXML(&b)
	return b.Bytes()
}

// Document writes a whole XML document in UTF-8 with a declaration.
func (n *Node) Document() []byte {
	var b bytes.Buffer
	b.WriteString("<?xml version='1.0' encoding='UTF-8'?>\n")
	n.writeXML(&b)
	b.WriteByte('\n')
	return b.Bytes()
}

func (n *Node) writeXML(b *bytes.Buffer) {
	b.WriteByte('<')
	b.WriteString(n.Name)
	for _, a := range n.Attrs {
		b.WriteByte(' ')
		b.WriteString(a.Name)
		b.WriteString(`="`)
		xml.EscapeText(b, []byte(a.Value))
		b.WriteByte('"')
	}
	if n.Text == "" && len(n.Children) == 0 {
		b.WriteString("/>")
		return
	}
	b.WriteByte('>')
	xml.EscapeText(b, []byte(n.Text))
	for _, c := range n.Children {
		c.writeXML(b)
	}
	b.WriteString("</")
	b.WriteString(n.Name)
	b.WriteByte('>')
}
