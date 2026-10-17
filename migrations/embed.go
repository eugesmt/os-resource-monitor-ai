package migrations

import "embed"

// FS contains the ordered SQL migrations shipped with the standalone binary.
//
// Keep migration files in this package so application code can use the same
// files as the Goose CLI without copying or rewriting their contents.
//
//go:embed *.sql
var FS embed.FS
