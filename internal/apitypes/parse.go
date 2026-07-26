package apitypes

import (
	"encoding/json"
	"fmt"
)

// ParseIndex parses raw JSON bytes into an Index.
func ParseIndex(data []byte) (*Index, error) {
	var idx Index
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("parse index.json: %w", err)
	}
	return &idx, nil
}
