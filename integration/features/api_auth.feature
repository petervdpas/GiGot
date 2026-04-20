Feature: Token API
  As an administrator
  I want to issue and revoke tokens via the API
  So that I can manage client access

  Scenario: Issue a token via API
    Given the server is running
    And a regular account "alice" exists
    When I POST "/api/auth/token" with body '{"username":"alice"}'
    Then the response status should be 201
    And the JSON response "username" should be "alice"
    And the JSON response "token" should not be empty

  Scenario: Issue token with empty username is rejected
    Given the server is running
    When I POST "/api/auth/token" with body '{"username":""}'
    Then the response status should be 400

  Scenario: Issue token for an unknown account is rejected
    Given the server is running
    When I POST "/api/auth/token" with body '{"username":"nobody-registered"}'
    Then the response status should be 400
    And the response body should contain "no local account"

  Scenario: Revoke a token via API
    Given the server is running
    And a regular account "bob" exists
    And I POST "/api/auth/token" with body '{"username":"bob"}'
    And I save the JSON response "token" as "issued_token"
    When I DELETE "/api/auth/token" with saved token "issued_token"
    Then the response status should be 200
    And the response body should contain "token revoked"
