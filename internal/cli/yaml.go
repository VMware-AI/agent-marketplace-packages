package cli

import (
	"gopkg.in/yaml.v3"
)

// yamlUnmarshalImpl is split into its own file so that client.go does
// not pull yaml into the client package's imports surface — the indirection
// keeps client.go's import set minimal.
func yamlUnmarshalImpl(data []byte, v any) error {
	return yaml.Unmarshal(data, v)
}