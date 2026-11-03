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
	type image struct{ name, path string }
	var saved []image
	for _, a := range is.Fields.Attachment {
		if !strings.HasPrefix(a.MimeType, "image/") || a.Size > maxImageSize || len(saved) >= maxImages {
			continue
		}
		p := uniquePath(out, filepath.Base(a.Filename))
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
		if _, dup := media[a.Filename]; !dup {
			media[a.Filename] = p
		}
		saved = append(saved, image{a.Filename, p})
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
	for _, im := range saved {
		if !strings.Contains(md.String(), im.path) {
			unref = append(unref, fmt.Sprintf("![%s](%s)", im.name, im.path))
		}
	}
	if len(unref) > 0 {
		sort.Strings(unref)
		md.WriteString("## Other attached images\n\n" + strings.Join(unref, "\n") + "\n")
	}

	path := filepath.Join(out, "story.md")
	return path, os.WriteFile(path, []byte(md.String()), 0o644)
}

// uniquePath keeps two attachments with the same name from overwriting each other.
func uniquePath(dir, name string) string {
	p := filepath.Join(dir, name)
	ext := filepath.Ext(name)
	for i := 2; ; i++ {
		// Any Stat error ends the search: a name too long to stat would never become free.
		if _, err := os.Stat(p); err != nil {
			return p
		}
		p = filepath.Join(dir, fmt.Sprintf("%s (%d)%s", strings.TrimSuffix(name, ext), i, ext))
	}
}
