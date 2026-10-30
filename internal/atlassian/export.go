package atlassian

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	maxImages    = 30
	maxImageSize = 15 << 20
)

// ExportBrief writes <dir>/<KEY>/story.md with the description, rich-text custom
// fields, comments, parent and links, plus image attachments saved next to it so
// a Claude session can open them. The folder is rewritten on every export.
func (c *Client) ExportBrief(key, dir string) (string, error) {
	q := url.Values{"fields": {"*all"}, "expand": {"names"}}
	var raw struct {
		Fields map[string]json.RawMessage `json:"fields"`
		Names  map[string]string          `json:"names"`
	}
	if err := c.do("GET", "/rest/api/3/issue/"+key, q, nil, &raw); err != nil {
		return "", err
	}
	var is fullIssue
	b, _ := json.Marshal(map[string]any{"key": key, "fields": raw.Fields})
	if err := json.Unmarshal(b, &is); err != nil {
		return "", err
	}

	out := filepath.Join(dir, key)
	if err := os.RemoveAll(out); err != nil {
		return "", err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return "", err
	}

	media := map[string]string{}
	n := 0
	for _, a := range is.Fields.Attachment {
		if !strings.HasPrefix(a.MimeType, "image/") || a.Size > maxImageSize || n >= maxImages {
			continue
		}
		p := filepath.Join(out, filepath.Base(a.Filename))
		f, err := os.Create(p)
		if err != nil {
			continue
		}
		err = c.download(a.Content, f)
		f.Close()
		if err != nil {
			os.Remove(p)
			continue
		}
		media[a.Filename] = p
		n++
	}

	var md strings.Builder
	f := is.Fields
	fmt.Fprintf(&md, "# %s: %s\n\n", key, f.Summary)
	fmt.Fprintf(&md, "- Type: %s\n- Status: %s\n- Link: %s\n", f.IssueType.Name, f.Status.Name, c.IssueURL(key))
	if f.Parent != nil {
		fmt.Fprintf(&md, "- Parent: %s %s\n", f.Parent.Key, f.Parent.Fields.Summary)
	}
	md.WriteString("\n")

	if d := ADFToMarkdown(f.Description, media); d != "" {
		md.WriteString("## Description\n\n" + d + "\n\n")
	}

	var custom []string
	for id, v := range raw.Fields {
		if strings.HasPrefix(id, "customfield_") && isADF(v) {
			custom = append(custom, id)
		}
	}
	sort.Slice(custom, func(i, j int) bool { return raw.Names[custom[i]] < raw.Names[custom[j]] })
	for _, id := range custom {
		if body := ADFToMarkdown(raw.Fields[id], media); body != "" {
			md.WriteString("## " + raw.Names[id] + "\n\n" + body + "\n\n")
		}
	}

	if len(f.IssueLinks) > 0 {
		md.WriteString("## Linked issues\n\n")
		for _, l := range f.IssueLinks {
			if l.OutwardIssue != nil {
				fmt.Fprintf(&md, "- %s %s: %s (%s)\n", l.Type.Outward, l.OutwardIssue.Key, l.OutwardIssue.Fields.Summary, l.OutwardIssue.Fields.Status.Name)
			}
			if l.InwardIssue != nil {
				fmt.Fprintf(&md, "- %s %s: %s (%s)\n", l.Type.Inward, l.InwardIssue.Key, l.InwardIssue.Fields.Summary, l.InwardIssue.Fields.Status.Name)
			}
		}
		md.WriteString("\n")
	}

	if cs := f.Comment.Comments; len(cs) > 0 {
		md.WriteString("## Comments\n\n")
		for _, cm := range cs {
			fmt.Fprintf(&md, "### %s, %s\n\n%s\n\n", cm.Author.DisplayName, trimDate(cm.Created), ADFToMarkdown(cm.Body, media))
		}
	}

	var unref []string
	for name, p := range media {
		if !strings.Contains(md.String(), p) {
			unref = append(unref, fmt.Sprintf("![%s](%s)", name, p))
		}
	}
	if len(unref) > 0 {
		sort.Strings(unref)
		md.WriteString("## Other attached images\n\n" + strings.Join(unref, "\n") + "\n")
	}

	path := filepath.Join(out, "story.md")
	return path, os.WriteFile(path, []byte(md.String()), 0o644)
}
