Feature: Self-serve /api/me
  As a signed-in user (admin or regular)
  I want an endpoint that returns my profile and my subscription keys
  So that I can retrieve the keys I need to configure a Formidable
  client without involving an administrator out of band.

  Scenario: /api/me rejects unauthenticated callers
    Given the server is running
    When I GET "/api/me"
    Then the response status should be 401

  Scenario: Admin can fetch their own profile and subscription keys
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    When I log in as admin "alice" with password "hunter2"
    And I POST "/api/admin/tokens" with body '{"username":"alice"}'
    Then the response status should be 201
    When I GET "/api/me"
    Then the response status should be 200
    And the JSON response "username" should be "alice"
    And the JSON response "provider" should be "local"
    And the JSON response "role" should be "admin"
    And the response body should contain "\"username\":\"alice\""

  Scenario: /api/me filters to the caller's own subscriptions only
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    And a regular account "bob" exists
    When I log in as admin "alice" with password "hunter2"
    And I POST "/api/admin/tokens" with body '{"username":"alice"}'
    And I POST "/api/admin/tokens" with body '{"username":"bob"}'
    And I GET "/api/me"
    Then the response status should be 200
    And the response body should contain "\"username\":\"alice\""
    And the response body should not contain "\"username\":\"bob\""
