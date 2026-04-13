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
package main

import "github.com/petervdpas/GiGot/cmd/gigot"

func main() {
	gigot.Execute()
}
