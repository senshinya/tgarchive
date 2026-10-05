package telegraph

import (
	"encoding/json"
	"errors"
)

// Node is a Telegraph content node: a text string (Tag == "") or an element.
// It marshals back to Telegraph's own shape: "text" or {"tag","attrs","children"}.
type Node struct {
	Text     string
	Tag      string
	Attrs    map[string]string
	Children []Node
}

type element struct {
	Tag      string            `json:"tag"`
	Attrs    map[string]string `json:"attrs,omitempty"`
	Children []Node            `json:"children,omitempty"`
}

func (n Node) MarshalJSON() ([]byte, error) {
	if n.Tag == "" {
		return json.Marshal(n.Text)
	}
	return json.Marshal(element{Tag: n.Tag, Attrs: n.Attrs, Children: n.Children})
}

func (n *Node) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*n = Node{Text: s}
		return nil
	}
	var e struct {
		Tag      string         `json:"tag"`
		Attrs    map[string]any `json:"attrs"`
		Children []Node         `json:"children"`
	}
	if err := json.Unmarshal(b, &e); err != nil {
		return err
	}
	if e.Tag == "" {
		return errors.New("telegraph: element without tag")
	}
	*n = Node{Tag: e.Tag, Children: e.Children}
	for k, v := range e.Attrs {
		if s, ok := v.(string); ok {
			if n.Attrs == nil {
				n.Attrs = map[string]string{}
			}
			n.Attrs[k] = s
		}
	}
	return nil
}
