package examples

import _ "embed"

// The Mock service embeds only its API definition; frontend edits must not
// change the Mock image and interrupt independent development rebuilds.
//
//go:embed mock-api.yaml
var MockSpec []byte
