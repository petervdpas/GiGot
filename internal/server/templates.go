package server

import (
	"embed"
	"html/template"
)

//go:embed templates/*.html
var templatesFS embed.FS

// parseAdminPage parses templates/admin_base.html together with one
// page-specific template so the page's `{{define "title"}}`,
// `{{define "styles"}}`, `{{define "content"}}`, and
// `{{define "scripts"}}` blocks override the base's matching
// `{{block}}` defaults at render time. The base file is parsed
// FIRST so it becomes the receiver template — handlers can keep
// calling `tmpl.Execute(w, data)` without switching to
// `ExecuteTemplate`. Adding a new admin page is one line here plus
// one new HTML file with the four defines.
func parseAdminPage(name string) *template.Template {
	return template.Must(template.ParseFS(
		templatesFS,
		"templates/admin_base.html",
		"templates/"+name,
	))
}

// One template per admin page. Login lives at /admin (adminPageTmpl);
// authenticated pages each have their own: repositories, subscriptions,
// credentials. No SPA panel switching — each section is a peer URL.
// All admin pages now ride on admin_base.html — see parseAdminPage.
var (
	indexTmpl           = template.Must(template.ParseFS(templatesFS, "templates/index.html"))
	adminPageTmpl       = template.Must(template.ParseFS(templatesFS, "templates/admin.html"))
	registerTmpl        = template.Must(template.ParseFS(templatesFS, "templates/register.html"))
	repositoriesTmpl    = parseAdminPage("repositories.html")
	subscriptionsTmpl   = parseAdminPage("subscriptions.html")
	credentialsPageTmpl = parseAdminPage("credentials.html")
	tagsPageTmpl        = parseAdminPage("tags.html")
	accountsPageTmpl    = parseAdminPage("accounts.html")
	authPageTmpl        = parseAdminPage("auth.html")
	userPageTmpl        = parseAdminPage("user.html")
	helpTmpl            = template.Must(template.ParseFS(templatesFS, "templates/help.html"))
)
