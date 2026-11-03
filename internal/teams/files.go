package teams

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// File is a file attached to a message: a link into SharePoint or OneDrive.
type File struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type driveItem struct {
	ETag   string `json:"eTag"`
	Name   string `json:"name"`
	WebURL string `json:"webUrl"`
}

// attachmentID is the GUID Teams expects as a reference attachment's id: the
// one in the drive item's eTag, "{GUID},n".
func (d driveItem) attachmentID() string {
	id, _, _ := strings.Cut(strings.Trim(d.ETag, `"{`), "}")
	return strings.ToLower(id)
}

// uploadChunk is a multiple of 320 KiB, as upload sessions require.
var uploadChunk = 320 * 1024 * 32

// upload puts a local file under prefix (a drive path ending where the file
// name goes) and returns the new item. Files up to 4 MB go in one request,
// bigger ones through an upload session.
func (c *Client) upload(prefix, local string) (driveItem, error) {
	b, err := os.ReadFile(local)
	if err != nil {
		return driveItem{}, err
	}
	name := filepath.Base(local)
	target := prefix + url.PathEscape(name) + ":"
	var it driveItem
	if len(b) <= 4<<20 {
		out, err := c.send("PUT", c.graph+target+"/content", bytes.NewReader(b), map[string]string{"Content-Type": "application/octet-stream"}, true)
		if err != nil {
			return driveItem{}, err
		}
		return it, json.Unmarshal(out, &it)
	}
	var sess struct {
		UploadURL string `json:"uploadUrl"`
	}
	if err := c.getJSONPost(target+"/createUploadSession", map[string]any{"item": map[string]string{"@microsoft.graph.conflictBehavior": "rename"}}, &sess); err != nil {
		return driveItem{}, err
	}
	var out []byte
	for off := 0; off < len(b); off += uploadChunk {
		end := min(off+uploadChunk, len(b))
		out, err = c.send("PUT", sess.UploadURL, bytes.NewReader(b[off:end]), map[string]string{
			"Content-Range": fmt.Sprintf("bytes %d-%d/%d", off, end-1, len(b)),
		}, false)
		if err != nil {
			return driveItem{}, err
		}
	}
	return it, json.Unmarshal(out, &it)
}

func (c *Client) getJSONPost(path string, body, v any) error {
	b, err := c.do("POST", path, body)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// folderFor is the upload prefix for a file sent to r: your OneDrive's Teams
// chat files folder for a chat, the channel's own files folder for a channel.
func (c *Client) folderFor(r ref) (string, error) {
	if r.chat != "" {
		return "/me/drive/root:/Microsoft%20Teams%20Chat%20Files/", nil
	}
	var f struct {
		ID     string `json:"id"`
		Parent struct {
			DriveID string `json:"driveId"`
		} `json:"parentReference"`
	}
	if err := c.getJSON("/teams/"+url.PathEscape(r.team)+"/channels/"+url.PathEscape(r.channel)+"/filesFolder", &f); err != nil {
		return "", err
	}
	return "/drives/" + url.PathEscape(f.Parent.DriveID) + "/items/" + url.PathEscape(f.ID) + ":/", nil
}

// message builds a chatMessage body: the text, then each file as a reference
// attachment.
func (c *Client) message(r ref, text string, files []string) (map[string]any, error) {
	content := strings.ReplaceAll(html.EscapeString(text), "\n", "<br>")
	msg := map[string]any{}
	if len(files) > 0 {
		folder, err := c.folderFor(r)
		if err != nil {
			return nil, err
		}
		var atts []map[string]any
		for _, f := range files {
			it, err := c.upload(folder, f)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", filepath.Base(f), err)
			}
			id := it.attachmentID()
			atts = append(atts, map[string]any{"id": id, "contentType": "reference", "contentUrl": it.WebURL, "name": it.Name})
			content += `<attachment id="` + id + `"></attachment>`
		}
		msg["attachments"] = atts
	}
	msg["body"] = map[string]string{"contentType": "html", "content": content}
	return msg, nil
}

// Download saves a file shared in Teams (its SharePoint or OneDrive link) into
// dir and returns the path.
func (c *Client) Download(link, dir string) (string, error) {
	share := "u!" + strings.TrimRight(base64.URLEncoding.EncodeToString([]byte(link)), "=")
	var it driveItem
	if err := c.getJSON("/shares/"+share+"/driveItem?$select=name,webUrl,eTag", &it); err != nil {
		return "", err
	}
	if it.Name == "" || strings.ContainsAny(it.Name, `/\`) || it.Name == ".." {
		return "", errors.New("the share has no usable file name")
	}
	b, err := c.do("GET", "/shares/"+share+"/driveItem/content", nil)
	if err != nil {
		return "", err
	}
	p := filepath.Join(dir, it.Name)
	return p, os.WriteFile(p, b, 0o644)
}

type attachment struct {
	ContentType string `json:"contentType"`
	ContentURL  string `json:"contentUrl"`
	Name        string `json:"name"`
}

func files(atts []attachment) []File {
	var out []File
	for _, a := range atts {
		if a.ContentType == "reference" {
			out = append(out, File{Name: a.Name, URL: a.ContentURL})
		}
	}
	return out
}
