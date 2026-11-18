package render

import (
	"encoding/json"
	"html/template"
)

// JSONForScript encodes v for a <script type="application/json"> block.
// "<", ">", "&", U+2028 and U+2029 are escaped so data cannot close the tag.
func JSONForScript(v any) (template.HTML, error) {
	b, err := json.Marshal(v) // Marshal escapes <, >, & as < etc. by default
	if err != nil {
		return "", err
	}
	return template.HTML(b), nil
}
