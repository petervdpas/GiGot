Feature: Authentication
  As a GiGot administrator
  I want to control access with authentication
  So that only authorized clients can connect

  Scenario: Unauthenticated access when auth is disabled
    Given the server is running with auth disabled
    When I request "/api/health"
    Then the response status should be 200

  Scenario: Unauthenticated access rejected when auth is enabled
    Given the server is running with auth enabled
    When I request "/api/health" without a token
    Then the response status should be 401

  Scenario: Valid token grants access
    Given the server is running with auth enabled
    And a token is issued for user "alice" with roles "admin"
    When I request "/api/health" with that token
    Then the response status should be 200

  Scenario: Invalid token is rejected
    Given the server is running with auth enabled
    When I request "/api/health" with token "bogus-token-123"
    Then the response status should be 401

  Scenario: Revoked token is rejected
    Given the server is running with auth enabled
    And a token is issued for user "bob" with roles "reader"
    And that token is revoked
    When I request "/api/health" with that token
    Then the response status should be 401
