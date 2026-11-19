package utils

import (
	"bytes"
	"errors"
	"io"
	"regexp"

	"gopkg.in/yaml.v3"
)

// YamlCommentLines adds "# " at the beginning of each line.
func YamlCommentLines(content []byte) []byte {
	re := regexp.MustCompile("(?m)^")
	out := re.ReplaceAll(content, []byte("# "))
	return out
}

// UnmarshalStrict decodes YAML data into out, failing if any unknown fields are encountered.
func UnmarshalStrict(in []byte, out any) error {
	dec := yaml.NewDecoder(bytes.NewReader(in))
	dec.KnownFields(true)
	if err := dec.Decode(out); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	return nil
}
