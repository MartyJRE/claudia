package atlassian

import (
	"encoding/json"
	"testing"
)

func TestADFToMarkdown(t *testing.T) {
	doc := `{"type":"doc","content":[
		{"type":"heading","attrs":{"level":2},"content":[{"type":"text","text":"Scope"}]},
		{"type":"bulletList","content":[{"type":"listItem","content":[{"type":"paragraph","content":[
			{"type":"text","text":"new","marks":[{"type":"strong"}]},{"type":"text","text":" store "},
			{"type":"text","text":"docs","marks":[{"type":"link","attrs":{"href":"https://x"}}]}]}]}]},
		{"type":"paragraph","content":[{"type":"mention","attrs":{"id":"me","text":"@Alex"}},{"type":"text","text":" ping"}]},
		{"type":"mediaSingle","content":[{"type":"media","attrs":{"alt":"shot.png"}}]}]}`
	got := ADFToMarkdown(json.RawMessage(doc), map[string]string{"shot.png": "/tmp/shot.png"})
	want := "### Scope\n\n- **new** store [docs](https://x)\n\n@Alex ping\n\n![shot.png](/tmp/shot.png)"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if !mentions(json.RawMessage(doc), "me") || mentions(json.RawMessage(doc), "other") {
		t.Error("mention detection")
	}
}
