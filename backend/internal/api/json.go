package api

import "encoding/json"

// jsonUnmarshal exists so handlers can decode small on-disk documents without
// importing encoding/json into every file that needs one.
func jsonUnmarshal(raw []byte, v any) error { return json.Unmarshal(raw, v) }
