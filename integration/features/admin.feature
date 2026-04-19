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
    When I log in as admin "alice" with password "hunter2"
    And I GET "/api/admin/tokens"
    Then the JSON response "count" should be 0

    When I POST "/api/admin/tokens" with body '{"username":"client-1"}'
    Then the response status should be 201

    When I GET "/api/admin/tokens"
    Then the JSON response "count" should be 1

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
    And the response body should contain "Admin login"
