package review

import "encoding/json"

func jsonMarshal(v any) ([]byte, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func jsonUnmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }
