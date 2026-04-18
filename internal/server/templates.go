package server

import (
	"embed"
	"html/template"
)

//go:embed templates/*.html
var templatesFS embed.FS

var (
	indexTmpl     = template.Must(template.ParseFS(templatesFS, "templates/index.html"))
	adminPageTmpl = template.Must(template.ParseFS(templatesFS, "templates/admin.html"))
)
