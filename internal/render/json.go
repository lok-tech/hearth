package render

import (
	"encoding/json"
	"html/template"
)

// JSONForScript encodes v as JSON for a <script type="application/json"> block or an HTML attribute.
// "<", ">", "&", U+2028 and U+2029 are escaped so data cannot close the tag.
// It returns template.JS, not template.HTML: html/template inserts JS verbatim in a script and
// attribute-escapes it in an attribute, whereas HTML would be quoted a second time in a script.
func JSONForScript(v any) (template.JS, error) {
	b, err := json.Marshal(v) // Marshal escapes <, >, & as < etc. by default
	if err != nil {
		return "", err
	}
	return template.JS(b), nil
}
