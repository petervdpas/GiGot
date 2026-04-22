Feature: Accounts (admins + regulars)
  As an administrator
  I want to manage the humans known to this server from a single directory
  So that admins and regulars, local and external, live in one store with
  one role check

  Scenario: Register creates a regular account and the holder can log in nowhere by default
    Given the server is running
    When I POST "/api/register" with body '{"username":"peter","password":"hunter2","display_name":"Peter"}'
    Then the response status should be 201
    And the JSON response "role" should be "regular"
    And the JSON response "provider" should be "local"
    And the JSON response "has_password" should be true

  Scenario: Register rejects a duplicate username
    Given the server is running
    And a regular account "peter" exists
    When I POST "/api/register" with body '{"username":"peter","password":"pw"}'
    Then the response status should be 409

  Scenario: Register requires a password
    Given the server is running
    When I POST "/api/register" with body '{"username":"peter"}'
    Then the response status should be 400

  Scenario: Admin can list accounts via the admin API
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    When I log in as admin "alice" with password "hunter2"
    And I GET "/api/admin/accounts"
    Then the response status should be 200
    And the response body should contain "alice"

  Scenario: Admin can create a regular account via the admin API
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    When I log in as admin "alice" with password "hunter2"
    And I POST "/api/admin/accounts" with body '{"provider":"local","identifier":"bob","role":"regular"}'
    Then the response status should be 201
    And the JSON response "role" should be "regular"
    And the JSON response "has_password" should be false

  Scenario: Admin can promote a regular to admin
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    And a regular account "bob" exists
    When I log in as admin "alice" with password "hunter2"
    And I PATCH "/api/admin/accounts/local/bob" with body '{"role":"admin"}'
    Then the response status should be 200
    And the JSON response "role" should be "admin"

  Scenario: Admin can delete a regular account
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    And a regular account "bob" exists
    When I log in as admin "alice" with password "hunter2"
    And I DELETE "/api/admin/accounts/local/bob"
    Then the response status should be 204

  Scenario: Bind-to-account creates a regular account for a legacy token
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    When I log in as admin "alice" with password "hunter2"
    And a token is issued for user "orphan-legacy"
    And I save the current token as "legacy_tok"
    And I POST "/api/admin/tokens/bind" with body '{"token":"${legacy_tok}"}'
    Then the response status should be 200
    And the JSON response "identifier" should be "orphan-legacy"
    And the JSON response "role" should be "regular"

  Scenario: Admin can issue a subscription key bound to an OAuth account
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    And a regular account "petervdpas" exists on provider "github"
    And a repository "addresses" exists
    When I log in as admin "alice" with password "hunter2"
    And I POST "/api/admin/tokens" with body '{"username":"github:petervdpas","repo":"addresses"}'
    Then the response status should be 201
    And the JSON response "username" should be "github:petervdpas"

  Scenario: Issuing a scoped token for an unknown provider account is rejected
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    And a regular account "petervdpas" exists
    And a repository "addresses" exists
    When I log in as admin "alice" with password "hunter2"
    And I POST "/api/admin/tokens" with body '{"username":"github:petervdpas","repo":"addresses"}'
    Then the response status should be 400
    And the response body should contain "no github account"

  Scenario: Admin can GET the auth runtime snapshot
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    When I log in as admin "alice" with password "hunter2"
    And I GET "/api/admin/auth"
    Then the response status should be 200
    And the JSON response "allow_local" should be true

  Scenario: Admin can flip allow_local via PATCH, and /admin/login 404s afterwards
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    When I log in as admin "alice" with password "hunter2"
    And I PATCH "/api/admin/auth" with body '{"allow_local":false,"oauth":{"github":{},"entra":{},"microsoft":{}},"gateway":{}}'
    Then the response status should be 200
    And the JSON response "allow_local" should be false
    When I POST "/admin/login" with body '{"username":"alice","password":"hunter2"}'
    Then the response status should be 404

  Scenario: PATCH /api/admin/auth rejects a gateway with an unresolvable secret_ref
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    When I log in as admin "alice" with password "hunter2"
    And I PATCH "/api/admin/auth" with body '{"allow_local":true,"oauth":{"github":{},"entra":{},"microsoft":{}},"gateway":{"enabled":true,"user_header":"X-U","sig_header":"X-S","timestamp_header":"X-T","secret_ref":"does-not-exist","max_skew_seconds":300}}'
    Then the response status should be 400
    When I GET "/api/admin/auth"
    Then the response status should be 200
    And the JSON response "allow_local" should be true

  Scenario: subscription_count on accounts list reflects issued keys
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    And a regular account "bob" exists
    And a repository "addresses" exists
    When I log in as admin "alice" with password "hunter2"
    And I POST "/api/admin/tokens" with body '{"username":"bob","repo":"addresses"}'
    Then the response status should be 201
    When I GET "/api/admin/accounts"
    Then the response status should be 200
    And the response body should contain "\"subscription_count\":1"
