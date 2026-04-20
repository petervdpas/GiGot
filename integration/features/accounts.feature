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
