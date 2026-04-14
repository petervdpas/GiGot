Feature: Subscription token persistence
  As an administrator
  I want issued tokens to survive a server restart
  So that giving a client access is durable, not ephemeral

  Scenario: Issued tokens survive a restart
    Given the server is running with auth disabled
    When I POST "/api/auth/token" with body '{"username":"alice"}'
    And I save the JSON response "token" as "alice_token"
    And the server restarts with auth enabled
    And I request "/api/health" with saved token "alice_token"
    Then the response status should be 200

  Scenario: Revoked tokens stay revoked across a restart
    Given the server is running with auth disabled
    When I POST "/api/auth/token" with body '{"username":"bob"}'
    And I save the JSON response "token" as "bob_token"
    And I DELETE "/api/auth/token" with saved token "bob_token"
    And the server restarts with auth enabled
    And I request "/api/health" with saved token "bob_token"
    Then the response status should be 401
