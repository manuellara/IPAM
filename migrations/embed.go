package migrations

import "embed"

// FS contains the SQL migrations used to initialize the application database.
//
//go:embed *.sql
var FS embed.FS
