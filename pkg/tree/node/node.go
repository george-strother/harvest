/*
 * Copyright NetApp Inc, 2021 All rights reserved
 */

package node

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"github.com/netapp/harvest/v2/pkg/util"
	"regexp"
	"strings"
)

type Node struct {
	parent   *Node
	name     []byte
	XMLName  xml.Name
	Attrs    []xml.Attr `xml:",any,attr"`
	Content  []byte     `xml:",innerxml"`
	Children []*Node    `xml:",any"`
}

func New(name []byte) *Node {
	return &Node{name: name}
}

func NewS(name string) *Node {
	return New([]byte(name))
}

func NewXML(name []byte) *Node {
	return NewXMLS(string(name))
}

func NewXMLS(name string) *Node {
	// ugly solution to support xml
	return &Node{XMLName: xml.Name{Local: name}}
}

func (n *Node) GetXMLNameS() string {
	return n.XMLName.Local
}

func (n *Node) SetXMLNameS(name string) {
	n.XMLName = xml.Name{Local: name}
}

func (n *Node) GetName() []byte {
	if name := n.GetXMLNameS(); name != "" {
		return []byte(name)
	}
	return n.name
}

func (n *Node) GetNameS() string {
	if name := n.GetXMLNameS(); name != "" {
		return name
	}
	return string(n.name)
}

func (n *Node) SetName(name []byte) {
	n.name = name
}

func (n *Node) SetNameS(name string) {
	n.name = []byte(name)
}

func (n *Node) GetParent() *Node {
	return n.parent
}

func (n *Node) GetAttr(name string) (xml.Attr, bool) {
	var attr xml.Attr
	for _, attr = range n.Attrs {
		if attr.Name.Local == name {
			return attr, true
		}
	}
	return attr, false
}

func (n *Node) GetAttrValueS(name string) (string, bool) {
	if attr, ok := n.GetAttr(name); ok {
		return attr.Value, true
	}
	return "", false
}

func (n *Node) AddAttr(attr xml.Attr) {
	n.Attrs = append(n.Attrs, attr)
}

func (n *Node) NewAttrS(name, value string) {
	n.AddAttr(xml.Attr{Name: xml.Name{Local: name}, Value: value})
}

func (n *Node) GetChildren() []*Node {
	return n.Children
}

func (n *Node) GetChild(name []byte) *Node {
	for _, child := range n.Children {
		if bytes.Equal(child.GetName(), name) {
			return child
		}
	}
	return nil
}

func (n *Node) GetChildS(name string) *Node {
	for _, child := range n.Children {
		if child.GetNameS() == name {
			return child
		}
	}
	return nil
}

func (n *Node) HasChild(name []byte) bool {
	return n.GetChild(name) != nil
}

func (n *Node) HasChildS(name string) bool {
	return n.GetChildS(name) != nil
}

func (n *Node) PopChild(name []byte) *Node {
	for i, child := range n.Children {
		if bytes.Equal(child.GetName(), name) {
			n.Children[i] = n.Children[len(n.Children)-1]
			n.Children = n.Children[:len(n.Children)-1]
			return child
		}
	}
	return nil
}

func (n *Node) PopChildS(name string) *Node {
	return n.PopChild([]byte(name))
}

func (n *Node) NewChild(name, content []byte) *Node {
	var child *Node
	if n.GetXMLNameS() != "" {
		child = NewXML(name)
	} else {
		child = New(name)
	}
	child.parent = n
	child.Content = content
	n.AddChild(child)
	return child
}

func (n *Node) NewChildS(name, content string) *Node {
	return n.NewChild([]byte(name), []byte(content))
}

func (n *Node) AddChild(child *Node) {
	n.Children = append(n.Children, child)
}

func (n *Node) GetContent() []byte {
	if content := bytes.TrimSpace(n.Content); len(content) != 0 {
		if content[0] != '<' {
			return content
		}
	}
	return []byte("")
}

func (n *Node) GetContentS() string {
	return string(n.Content)
}

/*
func (n *Node) GetContentIfHas() []byte {
    content, _ := n.GetContent()
    return content
}

func (n *Node) GetContentIfHasS() string {
    return string(GetContentIfHas())
}*/

func (n *Node) GetChildContent(name []byte) []byte {
	if child := n.GetChild(name); child != nil {
		return child.GetContent()
	}
	return []byte("")
}

func (n *Node) GetChildContentS(name string) string {
	if child := n.GetChildS(name); child != nil {
		return child.GetContentS()
	}
	return ""
}

// GetChildByContent Compare child content
func (n *Node) GetChildByContent(content string) *Node {
	for _, child := range n.Children {
		if child.GetContentS() == content {
			return child
		}
	}
	return nil
}

func (n *Node) SetChildContentS(name, content string) {
	if child := n.GetChildS(name); child != nil {
		child.SetContentS(content)
	} else {
		n.NewChildS(name, content)
	}
}

func (n *Node) GetAllChildContentS() []string {
	content := make([]string, 0)
	for _, ch := range n.Children {
		content = append(content, ch.GetContentS())
	}
	return content
}

func (n *Node) GetAllChildNamesS() []string {
	names := make([]string, 0)
	for _, ch := range n.Children {
		names = append(names, ch.GetNameS())
	}
	return names
}

func (n *Node) SetContent(content []byte) {
	n.Content = content
}

func (n *Node) SetContentS(content string) {
	n.SetContent([]byte(content))
}

func (n *Node) Copy() *Node {
	var clone *Node
	if n.GetXMLNameS() != "" {
		clone = NewXML(n.GetName())
	} else {
		clone = New(n.GetName())
	}
	clone.SetContent(n.GetContent())
	for _, child := range n.Children {
		clone.Children = append(clone.Children, child.Copy())
	}
	return clone
}

func (n *Node) Union(source *Node) {
	if len(n.GetContent()) == 0 {
		n.SetContent(source.GetContent())
	}
	for _, child := range source.Children {
		if !n.HasChild(child.GetName()) {
			n.AddChild(child)
		} else if child.GetChildren() != nil {
			// union at child level
			n.GetChild(child.GetName()).Union(child)
		} else {
			// child template would take precedence over parent
			n.SetChildContentS(child.GetNameS(), child.GetContentS())
		}
	}
}

//fetchRoot return if a parent name ancestor exists
func (n *Node) searchAncestor(ancestor string) *Node {
	if n == nil {
		return nil
	}
	p := n.GetParent()
	if p == nil {
		return nil
	}
	if p != nil && p.GetNameS() == ancestor {
		return n
	}
	return p.searchAncestor(ancestor)
}

func (n *Node) PreprocessTemplate() {
	for _, child := range n.Children {
		mine := n.GetChild(child.GetName())
		if mine != nil && len(child.GetName()) > 0 {
			if mine.searchAncestor("LabelAgent") != nil {
				if len(mine.GetContentS()) > 0 {
					mine.NewChildS("", child.GetContentS())
					mine.SetContentS("")
				}
			}
			mine.PreprocessTemplate()
		}
	}
}

//Merge method will merge the subtemplate into the receiver, modifying the receiver in-place.
//skipOverwrite is a readonly list of keys that will not be overwritten in the receiver.
func (n *Node) Merge(subtemplate *Node, skipOverwrite []string) {
	if subtemplate == nil {
		return
	}
	if len(n.Content) == 0 {
		n.Content = subtemplate.Content
	}
	for _, child := range subtemplate.Children {
		mine := n.GetChild(child.GetName())
		if len(child.GetName()) == 0 {
			if mine != nil && mine.GetParent() != nil && mine.GetParent().GetChildByContent(child.GetContentS()) == nil {
				mine.GetParent().AddChild(child)
			} else {
				if n.GetChildByContent(child.GetContentS()) == nil {
					n.AddChild(child)
				}
			}
		} else if mine == nil {
			n.AddChild(child)
		} else {
			if mine.GetParent() != nil && util.Contains(skipOverwrite, mine.GetParent().GetNameS()) {
				mine.SetContentS(mine.GetContentS() + "," + child.GetContentS())
			} else {
				mine.SetContentS(child.GetContentS())
			}
			mine.Merge(child, skipOverwrite)
		}
	}
}

func (n *Node) UnmarshalXML(dec *xml.Decoder, root xml.StartElement) error {
	n.Attrs = root.Attr
	type node Node
	return dec.DecodeElement((*node)(n), &root)
}

func (n *Node) FlatList(list *[]string, prefix string) {
	if n == nil {
		return
	}
	if len(n.Children) == 0 {
		var sub string
		if len(prefix) > 0 {
			sub = prefix + " " + simpleName(n.GetContentS())
		} else {
			sub = simpleName(n.GetContentS())
		}
		*list = append(*list, sub)
	} else {
		nameS := n.GetNameS()
		if len(nameS) > 0 && nameS != "counters" {
			if prefix == "" {
				prefix = nameS
			} else {
				prefix += " " + nameS
			}
		}
		for _, child := range n.Children {
			child.FlatList(list, prefix)
		}
	}
}

var wordRegex = regexp.MustCompile(`(\w|-)+`)

// simpleName returns the first word in the string s
// ignoring non-word characters. see node_test for examples
func simpleName(s string) string {
	return wordRegex.FindString(s)
}

func (n *Node) Print(depth int) string {
	builder := strings.Builder{}
	n.printN(depth, &builder)
	return builder.String()
}

func (n *Node) printN(depth int, b *strings.Builder) {
	name := "* "
	content := " *"
	if n.GetNameS() != "" {
		name = n.GetNameS()
	}

	if len(n.GetContentS()) > 0 && n.GetContentS()[0] != '<' {
		content = n.GetContentS()
	}
	fname := fmt.Sprintf("%s[%s]", strings.Repeat("  ", depth), name)
	b.WriteString(fmt.Sprintf("%-50s - %35s\n", fname, content))
	for _, child := range n.Children {
		child.printN(depth+1, b)
	}
}

func (n *Node) SearchContent(prefix []string, paths [][]string) ([]string, bool) {

	//fmt.Printf("SearchContent: prefix=%v \t paths=%v\n", prefix, paths)

	var search func(*Node, []string)

	matches := make([]string, 0)

	search = func(node *Node, currentPath []string) {
		var newPath []string
		if len(currentPath) > 0 || prefix[0] == node.GetNameS() {
			newPath = append(currentPath, node.GetNameS())
		} else {
			newPath = make([]string, len(currentPath))
			copy(newPath, currentPath)
		}
		//fmt.Printf(" -> current_path=%v \t new_path=%v\n", currentPath, newPath)
		for _, path := range paths {
			if util.EqualStringSlice(newPath, path) {
				matches = append(matches, node.GetContentS())
				//fmt.Println("    MATCH!")
				break
			}
		}
		if len(newPath) < util.MaxLen(paths) {
			for _, child := range node.GetChildren() {
				search(child, newPath)
			}
		}
	}

	search(n, []string{})

	//fmt.Printf("matches (%d):\n%v\n", len(matches), matches)
	return matches, len(matches) > 0
}

func (n *Node) SearchChildren(path []string) []*Node {

	var search func(*Node, []string)

	matches := make([]*Node, 0)

	search = func(node *Node, currentPath []string) {
		var newPath []string
		if len(currentPath) > 0 || path[0] == node.GetNameS() {
			newPath = append(currentPath, node.GetNameS())
		} else {
			newPath = make([]string, len(currentPath))
			copy(newPath, currentPath)
		}
		if util.EqualStringSlice(newPath, path) {
			matches = append(matches, node)
		} else if len(newPath) < len(path) {
			for _, child := range node.GetChildren() {
				search(child, newPath)
			}
		}
	}
	search(n, []string{})
	return matches
}

func DecodeHTML(x string) string {
	x = strings.ReplaceAll(x, "&amp;", "&")
	x = strings.ReplaceAll(x, "&lt;", "<")
	x = strings.ReplaceAll(x, "&gt;", ">")
	x = strings.ReplaceAll(x, "&apos;", "'")
	x = strings.ReplaceAll(x, "&quot;", "\"")
	x = strings.ReplaceAll(x, " ", "_") // not escape char, but wanted
	x = strings.ReplaceAll(x, "-", "_")
	return x
}
