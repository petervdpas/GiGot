Feature: Admin subscription-key management
  As an administrator
  I want to log in and manage subscription keys from the browser
  So that I can grant and revoke access to Formidable clients

  Scenario: Login with wrong password is rejected
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    When I log in as admin "alice" with password "wrong"
    Then the response status should be 401

  Scenario: Login with correct password sets a session cookie
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    When I log in as admin "alice" with password "hunter2"
    Then the response status should be 200
    And the response sets a session cookie
    And the JSON response "username" should be "alice"

  Scenario: Listing tokens requires a session
    Given the server is running
    When I GET "/api/admin/tokens"
    Then the response status should be 401

  Scenario: Admin can list, issue, and revoke tokens
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    And a regular account "client-1" exists
    When I log in as admin "alice" with password "hunter2"
    And I GET "/api/admin/tokens"
    Then the JSON response "count" should be 0

    When I POST "/api/admin/tokens" with body '{"username":"client-1"}'
    Then the response status should be 201

    When I GET "/api/admin/tokens"
    Then the JSON response "count" should be 1

  Scenario: Issuing a token bound to a non-existent repo is rejected
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    And a regular account "client-1" exists
    When I log in as admin "alice" with password "hunter2"
    And I POST "/api/admin/tokens" with body '{"username":"client-1","repos":["ghost-repo"]}'
    Then the response status should be 400
    And the response body should contain "ghost-repo"

  Scenario: Issuing a token for an unknown account is rejected
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    When I log in as admin "alice" with password "hunter2"
    And I POST "/api/admin/tokens" with body '{"username":"not-registered"}'
    Then the response status should be 400
    And the response body should contain "no local account"

  Scenario: Issuing a token bound to an existing repo succeeds and echoes the allowlist
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    And a regular account "client-1" exists
    And a repository "addresses" exists
    When I log in as admin "alice" with password "hunter2"
    And I POST "/api/admin/tokens" with body '{"username":"client-1","repos":["addresses"]}'
    Then the response status should be 201
    And the response body should contain "addresses"

  Scenario: PATCH rescopes an existing token
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    And a regular account "client-1" exists
    And a repository "addresses" exists
    And a repository "projects" exists
    When I log in as admin "alice" with password "hunter2"
    And I POST "/api/admin/tokens" with body '{"username":"client-1","repos":["addresses"]}'
    And I save the JSON response "token" as "tok"
    And I PATCH "/api/admin/tokens" with body '{"token":"${tok}","repos":["projects"]}'
    Then the response status should be 200
    When I GET "/api/admin/tokens"
    Then the response body should contain "projects"
    And the response body should not contain "addresses"

  Scenario: GET /api/admin/session echoes the logged-in username
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    When I log in as admin "alice" with password "hunter2"
    And I GET "/api/admin/session"
    Then the response status should be 200
    And the JSON response "username" should be "alice"

  Scenario: Admin routes reject a bearer-only request (no session cookie)
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    And a regular account "client-1" exists
    And a repository "addresses" exists
    When I log in as admin "alice" with password "hunter2"
    And I POST "/api/admin/tokens" with body '{"username":"client-1","repos":["addresses"]}'
    And I save the JSON response "token" as "bearer"
    And I POST "/admin/logout" with body ''
    And I request "/api/admin/tokens" with saved token "bearer"
    Then the response status should be 401

  Scenario: Admin logout invalidates the session
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    When I log in as admin "alice" with password "hunter2"
    And I POST "/admin/logout" with body ''
    And I GET "/api/admin/tokens"
    Then the response status should be 401

  Scenario: Admin accounts survive a server restart
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    When the server restarts
    And I log in as admin "alice" with password "hunter2"
    Then the response status should be 200

  Scenario: Admin session survives a server restart
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    When I log in as admin "alice" with password "hunter2"
    And the server restarts
    And I GET "/api/admin/tokens"
    Then the response status should be 200

  Scenario: Logout persists across a restart
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    When I log in as admin "alice" with password "hunter2"
    And I POST "/admin/logout" with body ''
    And the server restarts
    And I GET "/api/admin/tokens"
    Then the response status should be 401

  Scenario: Admin login page is served as HTML
    Given the server is running
    When I GET "/admin"
    Then the response status should be 200
    And the response content type should contain "text/html"
    And the response body should contain "Sign in"
