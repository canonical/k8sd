package utils

import (
	"net/url"
	"strings"
)

// Path sets the path of the URL from one or more path parts.
// It appends each of the pathParts (escaped using url.PathEscape) prefixed with "/" to the URL path.
func Path(u *url.URL, pathParts ...string) *url.URL {
	var path, rawPath strings.Builder

	for _, pathPart := range pathParts {
		// Generate unencoded path.
		path.WriteString("/") // Build an absolute URL.
		path.WriteString(pathPart)

		// Generate encoded path hint (this will be used by u.URL.EncodedPath() to decide its methodology).
		rawPath.WriteString("/") // Build an absolute URL.
		rawPath.WriteString(url.PathEscape(pathPart))
	}

	u.Path = path.String()
	u.RawPath = rawPath.String()

	return u
}
