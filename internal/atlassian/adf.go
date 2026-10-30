package atlassian

import (
	"encoding/json"
	"fmt"
	"strings"
)

type adfNode struct {
	Type    string         `json:"type"`
	Text    string         `json:"text"`
	Attrs   map[string]any `json:"attrs"`
	Marks   []adfMark      `json:"marks"`
	Content []adfNode      `json:"content"`
}

type adfMark struct {
	Type  string         `json:"type"`
	Attrs map[string]any `json:"attrs"`
}

// ADFToMarkdown renders an Atlassian Document Format value. media maps attachment
// file names to local paths so images in the text point at the downloaded files.
func ADFToMarkdown(raw json.RawMessage, media map[string]string) string {
	var doc adfNode
	if len(raw) == 0 || json.Unmarshal(raw, &doc) != nil || doc.Type != "doc" {
		return ""
	}
	r := adfRenderer{media: media}
	var b strings.Builder
	r.blocks(&b, doc.Content, "")
	return strings.TrimSpace(b.String())
}

func isADF(raw json.RawMessage) bool {
	var probe struct {
		Type string `json:"type"`
	}
	return json.Unmarshal(raw, &probe) == nil && probe.Type == "doc"
}

type adfRenderer struct {
	media map[string]string
}

func (r adfRenderer) blocks(b *strings.Builder, nodes []adfNode, indent string) {
	for _, n := range nodes {
		r.block(b, n, indent)
	}
}

func (r adfRenderer) block(b *strings.Builder, n adfNode, indent string) {
	switch n.Type {
	case "paragraph":
		b.WriteString(indent + r.inline(n.Content) + "\n\n")
	case "heading":
		lvl := 2
		if l, ok := n.Attrs["level"].(float64); ok {
			lvl = int(l)
		}
		b.WriteString(strings.Repeat("#", lvl+1) + " " + r.inline(n.Content) + "\n\n")
	case "bulletList", "orderedList":
		for i, item := range n.Content {
			marker := "- "
			if n.Type == "orderedList" {
				marker = fmt.Sprintf("%d. ", i+1)
			}
			var ib strings.Builder
			r.blocks(&ib, item.Content, "")
			body := strings.TrimSpace(ib.String())
			pad := indent + strings.Repeat(" ", len(marker))
			body = strings.ReplaceAll(body, "\n", "\n"+pad)
			b.WriteString(indent + marker + body + "\n")
		}
		b.WriteString("\n")
	case "codeBlock":
		lang, _ := n.Attrs["language"].(string)
		b.WriteString("```" + lang + "\n" + plain(n.Content) + "\n```\n\n")
	case "blockquote":
		var qb strings.Builder
		r.blocks(&qb, n.Content, "")
		for _, l := range strings.Split(strings.TrimSpace(qb.String()), "\n") {
			b.WriteString("> " + l + "\n")
		}
		b.WriteString("\n")
	case "panel":
		kind, _ := n.Attrs["panelType"].(string)
		var pb strings.Builder
		r.blocks(&pb, n.Content, "")
		b.WriteString("> **" + strings.ToUpper(kind) + "**\n")
		for _, l := range strings.Split(strings.TrimSpace(pb.String()), "\n") {
			b.WriteString("> " + l + "\n")
		}
		b.WriteString("\n")
	case "rule":
		b.WriteString("---\n\n")
	case "mediaSingle", "mediaGroup":
		for _, m := range n.Content {
			b.WriteString(r.media1(m) + "\n")
		}
		b.WriteString("\n")
	case "table":
		r.table(b, n)
	case "expand", "nestedExpand":
		if t, _ := n.Attrs["title"].(string); t != "" {
			b.WriteString("**" + t + "**\n\n")
		}
		r.blocks(b, n.Content, indent)
	default:
		if len(n.Content) > 0 {
			r.blocks(b, n.Content, indent)
		} else if s := r.inline([]adfNode{n}); s != "" {
			b.WriteString(indent + s + "\n\n")
		}
	}
}

func (r adfRenderer) table(b *strings.Builder, n adfNode) {
	for i, row := range n.Content {
		var cells []string
		for _, c := range row.Content {
			var cb strings.Builder
			r.blocks(&cb, c.Content, "")
			cells = append(cells, strings.ReplaceAll(strings.TrimSpace(cb.String()), "\n", " "))
		}
		b.WriteString("| " + strings.Join(cells, " | ") + " |\n")
		if i == 0 {
			b.WriteString("|" + strings.Repeat(" --- |", len(cells)) + "\n")
		}
	}
	b.WriteString("\n")
}

func (r adfRenderer) media1(m adfNode) string {
	alt, _ := m.Attrs["alt"].(string)
	if p, ok := r.media[alt]; ok {
		return fmt.Sprintf("![%s](%s)", alt, p)
	}
	if alt != "" {
		return fmt.Sprintf("[image: %s]", alt)
	}
	return "[image]"
}

func (r adfRenderer) inline(nodes []adfNode) string {
	var b strings.Builder
	for _, n := range nodes {
		switch n.Type {
		case "text":
			b.WriteString(marked(n.Text, n.Marks))
		case "hardBreak":
			b.WriteString("  \n")
		case "mention":
			t, _ := n.Attrs["text"].(string)
			b.WriteString(t)
		case "emoji":
			t, _ := n.Attrs["text"].(string)
			if t == "" {
				t, _ = n.Attrs["shortName"].(string)
			}
			b.WriteString(t)
		case "inlineCard", "blockCard", "embedCard":
			u, _ := n.Attrs["url"].(string)
			b.WriteString("<" + u + ">")
		case "status":
			t, _ := n.Attrs["text"].(string)
			b.WriteString("`" + t + "`")
		case "date":
			t, _ := n.Attrs["timestamp"].(string)
			b.WriteString(t)
		case "media", "mediaInline":
			b.WriteString(r.media1(n))
		default:
			b.WriteString(r.inline(n.Content))
		}
	}
	return b.String()
}

func marked(s string, marks []adfMark) string {
	link := ""
	for _, m := range marks {
		switch m.Type {
		case "strong":
			s = "**" + s + "**"
		case "em":
			s = "_" + s + "_"
		case "code":
			s = "`" + s + "`"
		case "strike":
			s = "~~" + s + "~~"
		case "link":
			link, _ = m.Attrs["href"].(string)
		}
	}
	if link != "" {
		return "[" + s + "](" + link + ")"
	}
	return s
}

func plain(nodes []adfNode) string {
	var b strings.Builder
	for _, n := range nodes {
		b.WriteString(n.Text)
		b.WriteString(plain(n.Content))
	}
	return b.String()
}
