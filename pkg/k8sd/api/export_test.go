// Tests that need a real snap mock must live in package api_test to avoid
// import cycles, so this file bridges the gap by re-exporting unexported symbols.

package api

var RemoveNodeFromMicrocluster = removeNodeFromMicrocluster
