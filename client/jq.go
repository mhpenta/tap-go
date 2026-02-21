package client

import (
	"encoding/json"
	"fmt"

	"github.com/itchyny/gojq"
)

func JQ(data json.RawMessage, expr string) (json.RawMessage, error) {
	query, err := gojq.Parse(expr)
	if err != nil {
		return nil, fmt.Errorf("jq parse: %w", err)
	}

	var input any
	if err := json.Unmarshal(data, &input); err != nil {
		return nil, fmt.Errorf("jq unmarshal input: %w", err)
	}

	var results []any
	iter := query.Run(input)
	for {
		v, ok := iter.Next()
		if !ok {
			break
		}
		if err, ok := v.(error); ok {
			return nil, fmt.Errorf("jq eval: %w", err)
		}
		results = append(results, v)
	}

	if len(results) == 1 {
		return json.Marshal(results[0])
	}
	return json.Marshal(results)
}
