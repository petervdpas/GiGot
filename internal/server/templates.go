package server

import (
	"embed"
	"html/template"
)

//go:embed templates/*.html
var templatesFS embed.FS

// One template per admin page. Login lives at /admin (adminPageTmpl);
// authenticated pages each have their own: repositories, subscriptions,
// credentials. No SPA panel switching — each section is a peer URL.
var (
	indexTmpl           = template.Must(template.ParseFS(templatesFS, "templates/index.html"))
	adminPageTmpl       = template.Must(template.ParseFS(templatesFS, "templates/admin.html"))
	registerTmpl        = template.Must(template.ParseFS(templatesFS, "templates/register.html"))
	repositoriesTmpl    = template.Must(template.ParseFS(templatesFS, "templates/repositories.html"))
	subscriptionsTmpl   = template.Must(template.ParseFS(templatesFS, "templates/subscriptions.html"))
	credentialsPageTmpl = template.Must(template.ParseFS(templatesFS, "templates/credentials.html"))
	accountsPageTmpl    = template.Must(template.ParseFS(templatesFS, "templates/accounts.html"))
	authPageTmpl        = template.Must(template.ParseFS(templatesFS, "templates/auth.html"))
	userPageTmpl        = template.Must(template.ParseFS(templatesFS, "templates/user.html"))
	helpTmpl            = template.Must(template.ParseFS(templatesFS, "templates/help.html"))
)
