package atlassian

import (
	"encoding/json"
	"strings"
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

func TestADFBlocks(t *testing.T) {
	text := func(s string, marks ...string) string {
		var ms []string
		for _, m := range marks {
			ms = append(ms, `{"type":"`+m+`"}`)
		}
		return `{"type":"text","text":"` + s + `","marks":[` + strings.Join(ms, ",") + `]}`
	}
	para := func(inline ...string) string {
		return `{"type":"paragraph","content":[` + strings.Join(inline, ",") + `]}`
	}
	for name, c := range map[string]struct{ in, want string }{
		"heading default level": {`{"type":"heading","content":[` + text("T") + `]}`, "### T"},
		"heading level 1":       {`{"type":"heading","attrs":{"level":1},"content":[` + text("T") + `]}`, "## T"},
		"marks":                 {para(text("b", "strong"), text("i", "em"), text("c", "code"), text("s", "strike"), text("both", "strong", "em")), "**b**_i_`c`~~s~~_**both**_"},
		"ordered nested": {`{"type":"orderedList","content":[
			{"type":"listItem","content":[` + para(text("one")) + `,{"type":"bulletList","content":[{"type":"listItem","content":[` + para(text("sub")) + `]}]}]},
			{"type":"listItem","content":[` + para(text("two")) + `]}]}`, "1. one\n   \n   - sub\n2. two"},
		"code block":  {`{"type":"codeBlock","attrs":{"language":"go"},"content":[` + text("x := 1") + `]}`, "```go\nx := 1\n```"},
		"blockquote":  {`{"type":"blockquote","content":[` + para(text("a")) + "," + para(text("b")) + `]}`, "> a\n> \n> b"},
		"panel":       {`{"type":"panel","attrs":{"panelType":"warning"},"content":[` + para(text("careful")) + `]}`, "> **WARNING**\n> careful"},
		"rule":        {`{"type":"rule"}`, "---"},
		"expand":      {`{"type":"expand","attrs":{"title":"More"},"content":[` + para(text("hidden")) + `]}`, "**More**\n\nhidden"},
		"expand bare": {`{"type":"nestedExpand","content":[` + para(text("hidden")) + `]}`, "hidden"},
		"table": {`{"type":"table","content":[
			{"type":"tableRow","content":[{"type":"tableHeader","content":[` + para(text("k")) + `]},{"type":"tableHeader","content":[` + para(text("v")) + `]}]},
			{"type":"tableRow","content":[{"type":"tableCell","content":[` + para(text("a")) + `]},{"type":"tableCell","content":[` + para(text("b")) + "," + para(text("c")) + `]}]}]}`,
			"| k | v |\n| --- | --- |\n| a | b  c |"},
		"media group": {`{"type":"mediaGroup","content":[{"type":"media","attrs":{"alt":"x.png"}},{"type":"media","attrs":{}}]}`, "[image: x.png]\n[image]"},
		"inline nodes": {para(`{"type":"hardBreak"}`, `{"type":"emoji","attrs":{"text":"😀"}}`, `{"type":"emoji","attrs":{"shortName":":ok:"}}`,
			`{"type":"inlineCard","attrs":{"url":"https://x"}}`, `{"type":"status","attrs":{"text":"DONE"}}`,
			`{"type":"date","attrs":{"timestamp":"1762160000000"}}`, `{"type":"mediaInline","attrs":{"alt":"y.png"}}`,
			`{"type":"unknownInline","content":[`+text("inner")+`]}`), "😀:ok:<https://x>`DONE`1762160000000[image: y.png]inner"},
		"unknown block with content": {`{"type":"layoutSection","content":[` + para(text("col")) + `]}`, "col"},
		"bare inline at block level": {`{"type":"blockCard","attrs":{"url":"https://card"}}`, "<https://card>"},
	} {
		got := ADFToMarkdown(json.RawMessage(`{"type":"doc","content":[`+c.in+`]}`), nil)
		if got != c.want {
			t.Errorf("%s: got %q want %q", name, got, c.want)
		}
	}
}

func TestADFNotADoc(t *testing.T) {
	for _, in := range []string{``, `"plain"`, `{"type":"paragraph"}`, `{`} {
		if got := ADFToMarkdown(json.RawMessage(in), nil); got != "" {
			t.Errorf("%q: got %q", in, got)
		}
	}
	if isADF(json.RawMessage(`"x"`)) || !isADF(json.RawMessage(`{"type":"doc"}`)) {
		t.Error("isADF")
	}
	if mentions(json.RawMessage(`[`), "me") {
		t.Error("mention in broken body")
	}
}
