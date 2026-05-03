// @title GiGot API
// @version 0.1.0
// @description Git-backed sync server for Formidable — local-first, server-optional.
//
// @contact.name Peter van de Pas
// @license.name MIT
//
// @host localhost:3417
// @BasePath /api
//
// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// @description Enter your bearer token as: Bearer <token>
//
// @securityDefinitions.basic BasicAuth
// @description HTTP Basic with the subscription token as the password. The username is ignored — tokens are self-identifying. This is the form `git clone http://user:<token>@host/git/repo` produces, so git-over-HTTP works out of the box.
//
// @securityDefinitions.apikey SessionAuth
// @in cookie
// @name gigot_session
// @description Session cookie minted by POST /api/admin/login (or any successful OAuth callback). Required by every /api/admin/* endpoint and the /fragments/* template server. Browsers attach it automatically; programmatic callers must include the cookie header on every request.
package main

import "github.com/petervdpas/GiGot/internal/cli"

// appVersion is overridden at build time via
//
//	-ldflags "-X main.appVersion=${VERSION}"
//
// (see .github/workflows/release.yml and the Dockerfile build stage).
// The sentinel default makes a plain `go build .` produce a binary
// whose -version output flags the missing ldflag obviously, instead of
// a silent empty string.
var appVersion = "0.0.0-dev"

func main() {
	cli.Execute(resolveVersion())
}
