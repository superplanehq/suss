package plan

import (
	"bytes"
	"encoding/json"
)

// MarshalCanonical sorts the document, checks emit-time invariants, and
// returns indented JSON with a trailing newline. HTML characters are not
// escaped, so command text round-trips exactly.
func (d Document) MarshalCanonical() ([]byte, error) {
	d.Sort()
	if err := d.Validate(); err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(d); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
